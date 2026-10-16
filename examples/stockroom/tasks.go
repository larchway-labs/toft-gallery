package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The scheduled half.
//
// A Toft scheduled task is an HTTP trigger, never a command: on its
// schedule the platform requests a path this app published, with the task's
// parameters, and records what came back. Three consequences shape this handler:
//
//   - The response body IS the run's result, capped and stored on the run row.
//     So it is written for somebody reading the run afterwards, not for a
//     machine — and a failure returns 500 with a body saying why, because a 500
//     whose body explains itself is the useful half of a failed run.
//   - The work has to finish inside the request. Returning 202 and carrying on
//     in a goroutine would record success for work that has not happened, which
//     is the trap the platform's own copy warns about.
//   - The platform captures no log here and cannot — this task's prints land in
//     the app's process log interleaved with every other request. So the lines
//     worth keeping are pushed to the run explicitly.
func (s *Server) handleScheduledReport(w http.ResponseWriter, r *http.Request) {
	runID := r.Header.Get("X-Toft-Run")
	schedule := r.Header.Get("X-Toft-Schedule")
	scheduled := r.Header.Get("X-Toft-Cron") != ""

	params := taskParams(r)
	kind := reportKind(params["report"])
	channel := strings.ToLower(strings.TrimSpace(params["channel"]))
	if channel == "" {
		channel = "slack"
	}
	to := params["to"]

	start := time.Now()
	trigger := "manual"
	if scheduled {
		trigger = "scheduled"
	}

	s.plat.TaskLog(r.Context(), runID, fmt.Sprintf(
		"stockroom: building %q for %s", kind.Title, channel))

	report, err := s.store.BuildReport(kind.Key, s.cfg.AppName)
	if err != nil {
		s.plat.TaskLog(r.Context(), runID, "! building the report failed: "+err.Error())
		taskFail(w, "the report would not build: "+err.Error())
		return
	}

	var results []deliverResult
	for _, ch := range channels(channel) {
		// The run id is the idempotency key. A retry of the same run must not
		// post the same report twice, and two different runs of the same
		// schedule must not collapse into one.
		key := fmt.Sprintf("stockroom:run:%s:%s", runID, ch)
		if runID == "" {
			key = fmt.Sprintf("stockroom:%s:%s:%s:%d", kind.Key, ch, to, time.Now().Unix()/60)
		}
		res := s.deliver(r.Context(), report, ch, to, trigger, runID, key)
		results = append(results, res)
		s.plat.TaskLog(r.Context(), runID, res.logs...)
	}

	s.store.Log("the schedule", "sent a report", kind.Title,
		fmt.Sprintf("%s, %s", channel, strings.Join(outcomes(results), "; ")))

	// The body the run stores. Written as a few readable lines, because this is
	// what somebody expanding the run in the Automation panel will read — the
	// app is not alive to explain itself later.
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", kind.Title)
	if schedule != "" {
		fmt.Fprintf(&b, "schedule: %s\n", schedule)
	}
	fmt.Fprintf(&b, "%d lines, %s at cost, %d at or below reorder point\n",
		report.Stats.Items, money(report.Stats.ValueCents), report.Stats.Low)
	for i, res := range results {
		fmt.Fprintf(&b, "%s: %s\n", channels(channel)[i], res.message)
	}
	fmt.Fprintf(&b, "took %s", time.Since(start).Round(time.Millisecond))

	failed := 0
	for _, res := range results {
		if !res.ok {
			failed++
		}
	}
	// Anything but 2xx–3xx is a failed run, and that is the honest answer when a
	// send did not happen: a task that returns 200 after failing to post is a
	// schedule that looks healthy for months while nobody receives anything.
	if failed == len(results) && failed > 0 {
		s.plat.TaskLog(r.Context(), runID, "! nothing was delivered")
		taskFail(w, b.String())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if failed > 0 {
		// Partial delivery still counts as a failure. "One of the two channels
		// worked" is not a result anybody should have to notice for themselves.
		w.WriteHeader(http.StatusInternalServerError)
	}
	_, _ = io.WriteString(w, b.String())
}

func taskFail(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, body)
}

// channels turns the `channel` parameter into the list to send on. "both" is
// spelled out rather than accepting a comma list, so a typo is a refusal
// somebody sees rather than a silently skipped recipient.
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

// taskParams reads the task's parameters from wherever the platform put them.
//
// A GET schedule merges them into the query string; a POST schedule sends them
// as the JSON body. Both, because the task is configured in the UI and whoever
// configures it picks the verb — an app that read only one would work until
// somebody chose the other and then fail with nothing in the log to explain it.
//
// Values are read as strings and never interpreted beyond that. The platform
// treats them as opaque; so does this.
func taskParams(r *http.Request) map[string]string {
	out := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	if r.Method == http.MethodPost && r.Body != nil {
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err == nil {
			for k, v := range body {
				switch t := v.(type) {
				case string:
					out[k] = t
				case float64:
					out[k] = fmt.Sprintf("%g", t)
				case bool:
					out[k] = fmt.Sprintf("%t", t)
				}
			}
		}
	}
	return out
}
