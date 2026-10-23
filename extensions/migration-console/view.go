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
		Body: "It is a job declared in this app's toft.toml, so it arrived with the deploy and there " +
			"is nothing to add. It starts approved migrations, watches their builds on the far side " +
			"and re-tests every target once a day.",
	}
	if at, why, ok := strings.Cut(refused, " "); ok && at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			step.Body += " Toft refused to start it " + ago(t) + ": " + why +
				" Until an administrator switches jobs back on, no approved migration will go."
			return step
		}
	}
	run, err := c.lastTickRun(ctx)
	switch {
	case err != nil:
		step.Unknown = true
		step.Body += " Its runs could not be read (" + err.Error() + ")."
	case run == nil:
		step.Unknown = true
		step.Body += " It has not run yet — the first fires within five minutes of the deploy. If it " +
			"never does, Run the tick says why."
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
			step.Body += " The last run was " + when + ", which is longer than the schedule allows — " +
				"it may be paused on the Automation tab, until the next deploy."
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
		Title: "Let the people who raise requests reach this app",
		Body: "A newly installed extension is private, which is its owner and this install's " +
			"administrators and nobody else. Everyone who raises or reviews a migration needs to " +
			"open this app, so a Toft administrator sets its visibility to signed-in " +
			"viewers. Nothing here is public: the platform still decides who is signed in, and " +
			"this app still refuses anybody the roster does not name.",
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
			Body: "Without it the console cannot tell who raised or approved anything, and a ledger " +
				"that cannot name an approver is not a record. A Toft administrator switches it " +
				"on from this app’s settings.",
			Done: !caller.IdentityOff && caller.Viewer != nil,
		},
		{
			Title: "Add this install as the source, with a read-scoped deploy key",
			Body: "Mint a deploy key here with the read scope and paste it in on Targets. Read-scoped " +
				"on purpose: the credential the console holds on the install it is running on can " +
				"pull a release and cannot deploy anything — including to this app.",
			Done: source != nil,
		},
		{
			Title: "Add a target, with a deploy-scoped key minted on that install",
			Body: "No credential travels between installs on its own and no Toft can mint one " +
				"for another, so an administrator on the target mints it and pastes it here.",
			Done: pushTargets > 0,
		},
		{
			Title: "Name at least one reviewer",
			Body: "The roster is this app’s own authority. A Toft administrator is not " +
				"automatically a reviewer: an app that could not refuse an administrator in its own " +
				"domain would have no domain.",
			Done: len(reviewers) > 0,
		},
		reach,
		schedule,
		{
			Title:    "Attach an email connection (optional)",
			Body:     "With one, the console can tell a requester when their migration finishes and mail the administrators’ group when something fails. Without one, none of that appears in the interface at all.",
			Done:     caps["email"],
			Optional: true,
		},
	}
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
