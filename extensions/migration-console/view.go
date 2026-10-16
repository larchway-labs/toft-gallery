package main

import (
	"context"
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
	// Unknown means this app cannot see whether it was done. Exactly one step
	// is: the scheduled task lives in the platform's own table and nothing an
	// app may call reports it. It renders as *not recorded* rather than as
	// outstanding, because claiming a step is missing when you cannot see it is
	// the same error as claiming it is done.
	Unknown bool
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

	// The scheduled task lives in the platform's own table and nothing an app
	// may call reports it, so this step used to be permanently *not recorded*.
	// It still is not read from the platform — but a tick that arrives is
	// evidence of one, and evidence is worth more here than a shrug: an install
	// whose administrator forgot this step has a console where every approval
	// sits at "it will start on the next tick" forever, and nothing anywhere
	// says why.
	settings, _ := c.store.Settings(ctx)
	schedule := SetupStep{
		Title: "Add a scheduled task pointing at /tasks/tick",
		Body: "It runs due migrations, watches builds on the far side and re-tests every target " +
			"once a day. It is also what keeps this app awake, because the idle reaper skips an " +
			"app with an enabled schedule.",
		Unknown: true,
	}
	if last, err := time.Parse(time.RFC3339, settings[SettingLastTickAt]); err == nil {
		if since := time.Since(last); since < time.Hour {
			schedule.Unknown, schedule.Done = false, true
			schedule.Body += " Last called " + ago(last) + "."
		} else {
			schedule.Body += " Last called " + ago(last) +
				", which is longer ago than any sensible schedule — check that it is still enabled."
		}
	} else {
		schedule.Body += " Nothing has called it yet, so either it has not been added or it has " +
			"never run."
	}

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
