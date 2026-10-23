package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The scheduled half: `toft.toml` declares it as the `report` job, and it is
// this same binary started as `toft-app report`.
//
// A job is a process of its own, not a request to this app's web server. Three
// consequences shape this file:
//
//   - **Its outcome is its exit code.** Zero is a succeeded run, anything else a
//     failed one, and the last line on stderr is the reason Toft shows beside
//     it. So a send that did not happen exits 1 — a report job that exits 0
//     after failing to post is a schedule that looks healthy for months while
//     nobody receives anything.
//   - **Its log is its stdout and stderr.** The runner ships both; there is
//     nothing to push and nothing to lose when the run is killed at its deadline.
//   - **It holds a run token, not the app's.** Accepted on the send routes for
//     as long as the run is in flight and nowhere else — so it cannot read
//     /app/self, and `deliver` is told so rather than asking and being refused.
//
// The params arrive typed and already checked: `report` and `channel` are one of
// their declared choices before this process starts, or the run never queued.

type reportParams struct {
	Report  string `json:"report"`
	Channel string `json:"channel"`
	To      string `json:"to"`
}

// runReportJob is the job's main. It returns the process's exit code.
func runReportJob(cfg Config) int {
	log := func(format string, a ...any) { fmt.Fprintf(os.Stdout, format+"\n", a...) }
	fail := func(format string, a ...any) int {
		// The last stderr line is the run's reason, so it is a sentence.
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		return 1
	}

	var p reportParams
	if raw := os.Getenv("TOFT_PARAMS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return fail("TOFT_PARAMS is not the JSON object the platform sends: %v", err)
		}
	}
	kind := reportKind(p.Report)
	channel := strings.ToLower(strings.TrimSpace(p.Channel))
	if channel == "" {
		channel = "slack"
	}

	runID := os.Getenv("TOFT_RUN_ID")
	trigger := "job"
	actor := "the report job"
	if os.Getenv("TOFT_TRIGGER") == "schedule" {
		trigger, actor = "scheduled", "the schedule"
	}
	if at := os.Getenv("TOFT_SCHEDULED_FOR"); at != "" {
		log("run %s, for the %s tick", shortID(runID), at)
	} else {
		log("run %s, started by %s", shortID(runID), orDash(os.Getenv("TOFT_TRIGGER")))
	}

	store, err := openStore(cfg.DataDir)
	if err != nil {
		return fail("the register would not open: %v", err)
	}
	defer store.Close()
	if err := store.SeedIfEmpty(); err != nil {
		return fail("seeding the register: %v", err)
	}
	// Said on every run, because it decides whether the report is the one the
	// web app shows: a job's scratch directory is its own, never the web
	// process's, so without a writable mount this is a freshly seeded register.
	log("register: %s — %s", cfg.DataDir, cfg.DataNote)

	s := &Server{cfg: cfg, store: store, plat: newJobPlatform(cfg)}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	s.progress(ctx, 10, "building "+kind.Title)
	report, err := store.BuildReport(kind.Key, cfg.AppName)
	if err != nil {
		return fail("the report would not build: %v", err)
	}
	log("%s: %d lines, %s at cost, %d at or below reorder point",
		kind.Title, report.Stats.Items, money(report.Stats.ValueCents), report.Stats.Low)

	var results []deliverResult
	for i, ch := range channels(channel) {
		s.progress(ctx, 40+i*30, "sending on "+ch)
		// The run id is the idempotency key. A rerun is a new run with a new id
		// and sends again, which is what somebody asking for a rerun wants; the
		// same run reaching the send twice does not post twice.
		key := fmt.Sprintf("stockroom:run:%s:%s", runID, ch)
		res := s.deliver(ctx, report, ch, p.To, trigger, runID, key)
		results = append(results, res)
		for _, l := range res.logs {
			if strings.HasPrefix(l, "!") {
				fmt.Fprintln(os.Stderr, strings.TrimSpace(strings.TrimPrefix(l, "!")))
			} else {
				log("%s", l)
			}
		}
	}
	store.Log(actor, "sent a report", kind.Title,
		fmt.Sprintf("%s, %s", channel, strings.Join(outcomes(results), "; ")))

	failed := 0
	for _, res := range results {
		if !res.ok {
			failed++
		}
	}
	summary := fmt.Sprintf("%s on %s: %s", kind.Title, channel, strings.Join(outcomes(results), ", "))
	if failed > 0 {
		// Partial delivery is a failure too. "One of the two channels worked" is
		// not a result anybody should have to notice for themselves.
		return fail("%s — %s", summary, results[firstFailed(results)].message)
	}
	s.progress(ctx, 100, summary)
	log("%s", summary)
	return 0
}

// progress moves the bar on the run's status page. A failure to report it is
// logged and ignored: telemetry never fails the work it describes.
func (s *Server) progress(ctx context.Context, percent int, message string) {
	base, token := s.cfg.APIURL, os.Getenv("TOFT_JOB_TOKEN")
	if base == "" || token == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"version": runnerContractVersion, "percent": percent, "message": message,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/v1/runner/progress", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Toft-Runner-Version", runnerContractVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stdout, "(progress not recorded: %v)\n", err)
		return
	}
	resp.Body.Close()
}

// runnerContractVersion is the runner callback contract the progress body names.
const runnerContractVersion = "2"

// channels turns the `channel` parameter into the list to send on. "both" is a
// declared choice rather than a comma list, so a typo is refused when the run
// is started rather than becoming a silently skipped recipient.
func channels(channel string) []string {
	switch channel {
	case "both":
		return []string{"slack", "email"}
	case "email":
		return []string{"email"}
	default:
		return []string{"slack"}
	}
}

func outcomes(results []deliverResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.ok {
			out = append(out, "sent")
		} else {
			out = append(out, "failed")
		}
	}
	return out
}

func firstFailed(results []deliverResult) int {
	for i, r := range results {
		if !r.ok {
			return i
		}
	}
	return 0
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return orDash(id)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
