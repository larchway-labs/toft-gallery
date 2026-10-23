package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// View is what every page template is handed: who is asking, what the install
// still needs, and the page's own data.
type View struct {
	Title  string
	Nav    string
	Caller Caller
	Setup  []SetupStep
	Caps   map[string]bool
	Notice *Notice
	Data   any
}

type Notice struct {
	Tone   string
	Title  string
	Body   string
	Action string
}

// SetupStep is one of the things a freshly-deployed extension still needs. The
// Gallery guarantees the catalog grants nothing, so this app is *deliberately*
// not working when it lands, and /health is where that is said rather than
// discovered at the first submit.
type SetupStep struct {
	Title string
	Body  string
	Done  bool
	// Optional means the install works without it. Exactly one step is.
	Optional bool
	// Unknown means this app cannot see whether it was done. It renders as
	// *not recorded* rather than as outstanding, because claiming a step is
	// missing when you cannot see it is the same error as claiming it is done.
	Unknown bool
}

// tickRun is the part of a job run the install page reads.
type tickRun struct {
	Status        string     `json:"status"`
	StartedBy     string     `json:"started_by"`
	QueuedAt      *time.Time `json:"queued_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	FailureReason string     `json:"failure_reason"`
}

// lastTickRun is the newest run of the tick job, read as the app — the
// platform's record, not this app's, so a tick that never reached the ledger
// is still seen.
func (c *Console) lastTickRun(ctx context.Context) (*tickRun, error) {
	if c.ticks != nil {
		if v, ok := c.ticks.get("", time.Now()); ok {
			return v, nil
		}
	}
	run, err := c.fetchLastTickRun(ctx)
	if err == nil && c.ticks != nil {
		c.ticks.put("", run, time.Now())
	}
	return run, err
}

func (c *Console) fetchLastTickRun(ctx context.Context) (*tickRun, error) {
	if c.cfg.AppToken == "" || c.cfg.APIURL == "" {
		return nil, fmt.Errorf("no platform token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.APIURL+"/api/v1/app/job-runs?scope=app&job=tick&limit=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AppToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	var body struct {
		Runs []tickRun `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Runs) == 0 {
		return nil, nil
	}
	return &body.Runs[0], nil
}

// tickStep is the install page's line about the tick.
func (c *Console) tickStep(ctx context.Context, refused string) SetupStep {
	step := SetupStep{
		Title: "The tick runs every five minutes",
		Body: "It comes with this app, so there’s nothing to set up. It starts approved migrations, " +
			"follows their builds on the target and re-tests every target once a day.",
	}
	if at, why, ok := strings.Cut(refused, " "); ok && at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			step.Body += " Toft refused to start it " + ago(t) + ": " + why +
				" No approved migration will run until an administrator turns jobs back on."
			return step
		}
	}
	run, err := c.lastTickRun(ctx)
	switch {
	case err != nil:
		step.Unknown = true
		step.Body += " Couldn’t read its runs (" + err.Error() + ")."
	case run == nil:
		step.Unknown = true
		step.Body += " It hasn’t run yet; the first run starts within five minutes of the deploy. If it " +
			"doesn’t, Run the tick now shows why."
	default:
		last := run.QueuedAt
		if run.FinishedAt != nil {
			last = run.FinishedAt
		}
		when := "recently"
		if last != nil {
			when = ago(*last)
		}
		switch {
		case run.Status == "failed" || run.Status == "timed_out" || run.Status == "out_of_memory":
			step.Body += " The last run, " + when + ", ended " + strings.ReplaceAll(run.Status, "_", " ") +
				": " + run.FailureReason + ". Its log is on the Automation tab."
		case last != nil && time.Since(*last) > time.Hour:
			step.Body += " The last run was " + when + ", longer ago than the schedule allows. " +
				"It may be paused on the Automation tab."
		default:
			step.Done = true
			step.Body += " Last ran " + when + ", " + strings.ReplaceAll(run.Status, "_", " ") + "."
		}
	}
	return step
}

func (c *Console) view(r *http.Request, title, nav string, caller Caller, data any) View {
	return View{Title: title, Nav: nav, Caller: caller, Data: data,
		Caps: c.capabilities(r.Context())}
}

// setupSteps is /health's content, and it is also what an empty state shows
// instead of pretending the app works.
func (c *Console) setupSteps(ctx context.Context, caller Caller, caps map[string]bool) []SetupStep {
	source, _ := c.store.SourceTarget(ctx)
	targets, _ := c.store.ListTargets(ctx)
	reviewers, _ := c.store.ListReviewers(ctx)

	// Whether anybody but an administrator can reach this app at all.
	//
	// A console installed from the Gallery lands `private`, which means its
	// owner and the install's administrators. They finish all six steps, tell
	// their developers to raise a request, and every one of those developers
	// gets the platform's 404 with nothing anywhere saying why. The console can
	// see this — the read-scoped key it already holds lists apps on this
	// install, and it knows its own id — so it says it rather than leaving it
	// to be discovered.
	reach := SetupStep{
		Title: "Set this app’s visibility to signed-in viewers",
		Body: "A new extension is private to its owner and administrators. A Toft administrator " +
			"sets this app’s visibility to signed-in viewers so everyone who raises or reviews a " +
			"migration can open it.",
		Unknown: true,
	}
	if source != nil && c.cfg.AppID != "" {
		if p, err := c.platformFor(source); err == nil {
			if self, err := p.App(ctx, c.cfg.AppID); err == nil {
				reach.Unknown = false
				reach.Done = self.Visibility != "private"
				if reach.Done {
					reach.Body += " Currently " + self.Visibility + "."
				}
			}
		}
	}

	// The tick is a job declared in toft.toml, so there is nothing to add — it
	// arrived with the deploy. What can still be wrong is that it is not
	// running: jobs switched off on this install, the schedule paused on the
	// Automation tab, or every run failing. The platform's own record of the
	// tick's runs answers the last two; the first is only ever seen as a refused
	// start, which startTick records.
	settings, _ := c.store.Settings(ctx)
	schedule := c.tickStep(ctx, settings[SettingTickRefused])

	pushTargets := 0
	for _, t := range targets {
		if t.Kind == TargetKindTarget {
			pushTargets++
		}
	}

	return []SetupStep{
		{
			Title: "Turn on viewer identity for this app",
			Body: "Without it the console can’t record who raised or approved a request. A Toft " +
				"administrator turns it on in this app’s settings.",
			Done: !caller.IdentityOff && caller.Viewer != nil,
		},
		{
			Title: "Add this install as the source, with a read-scoped deploy key",
			Body: "Mint a deploy key on this install with the read scope and add it on the Targets " +
				"page. The console uses it only to read releases.",
			Done: source != nil,
		},
		{
			Title: "Add a target, with a deploy-scoped key minted on that install",
			Body: "An administrator on the target install mints the key; then add it on the " +
				"Targets page.",
			Done: pushTargets > 0,
		},
		{
			Title: "Add at least one reviewer",
			Body: "Reviewers approve and reject requests. Toft administrators aren’t reviewers " +
				"unless they’re added on the Reviewers page.",
			Done: len(reviewers) > 0,
		},
		reach,
		schedule,
		{
			Title:    "Attach an email connection",
			Body:     "Lets the console email requesters when a migration finishes, and an administrators’ group when something fails.",
			Done:     caps["email"],
			Optional: true,
		},
	}
}

// refuseUnidentified answers 503 when Toft did not say who the caller is, and
// reports whether it did. A 503 is also what keeps an htmx poll from swapping
// a region for an error: htmx leaves the old content in place.
func (c *Console) refuseUnidentified(w http.ResponseWriter, r *http.Request, caller Caller) bool {
	if !caller.LookupFailed {
		return false
	}
	w.Header().Set("Retry-After", "5")
	c.refuse(w, r, caller, http.StatusServiceUnavailable, "Couldn’t confirm who you are",
		"Toft didn’t respond in time, usually because it’s busy. Nothing was changed. Try again in a few seconds.")
	return true
}

func (c *Console) refuse(w http.ResponseWriter, r *http.Request, caller Caller, status int, title, body string) {
	v := c.view(r, title, "", caller, nil)
	v.Notice = &Notice{Tone: "danger", Title: title, Body: body}
	c.render.page(w, status, "refused", v)
}

// redirect sends the browser somewhere after a mutation.
//
// Two header families, one helper, because the rule is asymmetric: the proxy
// re-adds the prefix to `Location`, and nothing rewrites `HX-Redirect`. Getting
// that backwards is invisible until a form posts.
func (c *Console) redirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", c.urls.Path(path))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, c.urls.Bare(path), http.StatusSeeOther)
}

func trimForm(r *http.Request, name string) string {
	return strings.TrimSpace(r.FormValue(name))
}
