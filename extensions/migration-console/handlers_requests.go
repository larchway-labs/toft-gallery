package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// --- The queue --------------------------------------------------------------

// QueueRow is one line of the home page, with everything it needs already
// resolved: a template that had to look a target up would be a template making
// database calls.
type QueueRow struct {
	Request
	TargetLabel string
	Mine        bool
	AwaitingMe  bool
}

type QueueData struct {
	Tab      string
	Rows     []QueueRow
	Counts   map[string]int
	Behind   []LinkState
	CanRaise bool
}

// The home page is a queue, not a dashboard.
//
// People arrive with one of three questions — what needs my decision, what did
// I ask for, what is moving right now — and a dashboard answers none of them.
// So the primary axis is a tab strip with counts, because "Awaiting me (3)" has
// to be one click during a release window. No charts: nobody opens a
// change-management tool to learn a rate.
func (c *Console) handleQueue(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if !caller.Submitter {
		c.refuse(w, r, caller, http.StatusForbidden, "You’re not signed in to this app",
			"Sign in to Toft and open the app again.")
		return
	}

	ctx := r.Context()
	all, err := c.store.ListRequests(ctx)
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t load requests", err.Error())
		return
	}
	targets, _ := c.store.ListTargets(ctx)
	labels := map[string]string{}
	for _, t := range targets {
		labels[t.ID] = t.Label
	}
	watching, _ := c.store.AppsSomeoneWasAddedTo(ctx, caller.ID())
	viewerOf := map[string]bool{}
	for _, rq := range all {
		vs, _ := c.store.ListRequestViewers(ctx, rq.ID)
		for _, v := range vs {
			if v.UserID == caller.ID() {
				viewerOf[rq.ID] = true
			}
		}
	}

	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "mine"
	}

	counts := map[string]int{}
	var rows []QueueRow
	for _, rq := range all {
		row := QueueRow{
			Request:     rq,
			TargetLabel: labels[rq.TargetID],
			// "Mine" is the ones I raised *and* the ones somebody added me to.
			// A person named on a request is watching it, which is the whole
			// reason they were named.
			Mine:       rq.RequesterID == caller.ID() || viewerOf[rq.ID],
			AwaitingMe: caller.Reviewer && rq.Status == StatusSubmitted && rq.RequesterID != caller.ID(),
		}
		// The queue shows a person what the request page would let them open
		// (requestAudience): the roster and the console's editors see every
		// request, everybody else the ones they raised or were named on.
		if !(caller.Editor || caller.Reviewer) && !row.Mine {
			continue
		}
		if row.Mine {
			counts["mine"]++
		}
		if row.AwaitingMe {
			counts["review"]++
		}
		if rq.Status == StatusApproved && rq.ScheduledFor != nil {
			counts["scheduled"]++
		}
		counts["all"]++
		rows = append(rows, row)
	}

	var shown []QueueRow
	for _, row := range rows {
		switch tab {
		case "mine":
			if row.Mine {
				shown = append(shown, row)
			}
		case "review":
			if row.AwaitingMe {
				shown = append(shown, row)
			}
		case "scheduled":
			if row.Status == StatusApproved && row.ScheduledFor != nil {
				shown = append(shown, row)
			}
		default:
			shown = append(shown, row)
		}
	}

	data := QueueData{Tab: tab, Rows: shown, Counts: counts, CanRaise: caller.Viewer != nil}
	// Links are the roster's and the editors' page (handleLinks), and this tab
	// is the same list.
	if tab == "behind" && (caller.Editor || caller.Reviewer) {
		data.Behind = c.linkStates(ctx, caller)
		counts["behind"] = 0
		for _, l := range data.Behind {
			if l.State == LinkBehind {
				counts["behind"]++
			}
		}
	}
	_ = watching

	v := c.view(r, "Requests", "queue", caller, data)
	v.Setup = c.setupSteps(ctx, caller, v.Caps)
	c.render.page(w, http.StatusOK, "queue", v)
}

// --- Raising one ------------------------------------------------------------

type NewRequestData struct {
	Apps     []App
	Targets  []Target
	Selected string
	Refusal  string
	// Watching names the apps this person can raise for because somebody added
	// them to a request, rather than because they own the app.
	Watching map[string]bool
	// NoteRequired is this install's house rule, carried onto the form so the
	// field is marked before it is refused rather than after.
	NoteRequired bool

	// The fields below carry a *previous* request onto a blank form — "raise
	// the same one again", offered on a request that failed, was rejected,
	// withdrawn or cancelled.
	//
	// It is a prefill and never a reopening. The new request is new: its own
	// row, its own reviewer, its own approval, and it re-checks that the person
	// may still raise for that app. What it saves is the retyping, which is the
	// whole of what was wrong before — a failed migration left the requester to
	// rebuild the app, the release, the target, the destination and the reason
	// from scratch, with the old request open in another tab to copy from.
	//
	// The release is carried by id and the picker still preselects it *only if
	// it is still there*: an approval is for particular bytes, and a release
	// pruned since is one this form must not silently substitute for.
	SelectedDeployment string
	SelectedTarget     string
	Reason             string
	Note               string
	DestSlug           string
	DestVisibility     string
	// Again names the request this one was prefilled from, so the page can say
	// so rather than appearing to have remembered something.
	Again string
}

// raisableApps is the apps the console will offer, and it is deliberately
// narrower than what this person can *read*.
//
// Toft has one definition of "readable apps" and it lives in auth.CanRead.
// An app that filtered the list by re-deriving the grant ladder would be a
// second definition, in a second language, drifting from the first. Ownership
// is the one predicate this console can compute from data it already holds —
// the app row's two owner columns and the viewer's group list — and it is
// strictly narrower than readability, so it can never offer somebody an app
// they may not see.
//
// The second half is this console's own: an app somebody was **named on** in a
// past request. That is not a re-derivation of anything on the platform — it is
// a fact this app recorded when a requester added them — and it is what makes
// "they can request future migrations" true rather than a sentence in a README.
func (c *Console) raisableApps(ctx context.Context, caller Caller) ([]App, map[string]bool, error) {
	source, err := c.store.SourceTarget(ctx)
	if err != nil || source == nil {
		return nil, nil, fmt.Errorf("this install hasn’t been added as the source yet; an administrator adds it on the Targets page")
	}
	p, err := c.platformFor(source)
	if err != nil {
		return nil, nil, err
	}
	apps, err := p.Apps(ctx)
	if err != nil {
		return nil, nil, err
	}
	watching, err := c.store.AppsSomeoneWasAddedTo(ctx, caller.ID())
	if err != nil {
		return nil, nil, err
	}
	groups := caller.GroupIDs()

	var out []App
	for _, a := range apps {
		owned := (a.OwnerID != "" && a.OwnerID == caller.ID()) ||
			(a.OwnerGroupID != "" && groups[a.OwnerGroupID])
		if owned || watching[a.ID] {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, watching, nil
}

func (c *Console) handleNewRequestForm(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil {
		c.refuse(w, r, caller, http.StatusForbidden, "The console can’t tell who you are",
			"Raising a request needs viewer identity, which is off for this app. A Toft administrator can turn it on.")
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	data := NewRequestData{
		Selected:           q.Get("app"),
		SelectedDeployment: q.Get("deployment"),
		SelectedTarget:     q.Get("target"),
		Reason:             q.Get("reason"),
		Note:               q.Get("note"),
		DestSlug:           q.Get("slug"),
		DestVisibility:     q.Get("visibility"),
		Again:              q.Get("again"),
	}
	// Every one of those is a *suggestion in a form field*, and none of them is
	// trusted: the app is re-checked against what this person may raise for,
	// the release is re-read from the source, and the target is re-read from
	// the console's own rows — all in handleCreateRequest, exactly as they are
	// for a form nobody prefilled. A query string is not an authorization
	// decision, and a link somebody was sent must not become one.
	if data.DestVisibility != "" && data.DestVisibility != "private" &&
		data.DestVisibility != "authenticated" && data.DestVisibility != "public" {
		data.DestVisibility = ""
	}
	apps, watching, err := c.raisableApps(ctx, caller)
	if err != nil {
		data.Refusal = err.Error()
	}
	data.Apps, data.Watching = apps, watching
	if settings, err := c.store.Settings(ctx); err == nil {
		data.NoteRequired = settings[SettingRequireNoteOnNew] == "true"
	}
	targets, _ := c.store.ListTargets(ctx)
	for _, t := range targets {
		if t.Kind == TargetKindTarget {
			data.Targets = append(data.Targets, t)
		}
	}
	v := c.view(r, "New request", "queue", caller, data)
	v.Setup = c.setupSteps(ctx, caller, v.Caps)
	c.render.page(w, http.StatusOK, "new", v)
}

// handleReleasePicker is the dialog's body, fetched when it opens rather than
// with the page: the difference between a form that appears and one that waits.
//
// Each row carries when it was deployed and by whom, the branch and short
// commit, whether it is **the release serving right now** — badged and
// preselected — and Toft's own dependency verdict for it. Ids alone would
// teach the submitter that they chose an app and the release was incidental,
// which is the exact misunderstanding this design exists to prevent.
func (c *Console) handleReleasePicker(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	// The name the form actually sends. The select is `name="app_id"` and the
	// fragment is fetched with hx-include, so reading anything else here is a
	// picker that never fills in.
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		c.render.fragment(w, "new", "releases", nil)
		return
	}
	// The release a "raise it again" link carried. It preselects a row and
	// nothing more: if that deployment has since been pruned it is simply not
	// in the list, and the picker falls back to badging what is serving now —
	// which is the honest outcome, because the bytes somebody approved are gone
	// and no other release is a substitute for them.
	want := r.URL.Query().Get("want")
	source, err := c.store.SourceTarget(r.Context())
	if err != nil || source == nil {
		c.render.fragment(w, "new", "releases", map[string]any{
			"Error": "This install hasn’t been added as the source yet."})
		return
	}
	p, err := c.platformFor(source)
	if err != nil {
		c.render.fragment(w, "new", "releases", map[string]any{"Error": err.Error()})
		return
	}
	deploys, err := p.Deployments(r.Context(), appID)
	if err != nil {
		c.render.fragment(w, "new", "releases", map[string]any{"Error": err.Error()})
		return
	}
	// The platform's own verdict is per app rather than per deployment, so it
	// is read once and shown against the release it names — the one that was
	// serving when it was scanned, which is not always the row marked current.
	// Attaching it to every row would claim a scan of releases nobody scanned.
	// A read that fails is nil, which every use below reads as not recorded.
	rep, _ := p.Dependencies(r.Context(), appID)

	var rows []ReleaseRow
	found := false
	for _, d := range deploys {
		if d.Status != "success" {
			continue
		}
		item := ReleaseRow{Deployment: d, Label: d.Label()}
		// With no release named, the reason there is none (off, never
		// scanned, no advisory data) belongs on the serving row, as before.
		if rep.Describes(d.ID) || (d.Current && (rep == nil || rep.Serving == nil)) {
			item.Verdict, item.Why = rep.VerdictFor(d.ID)
		}
		if want != "" && d.ID == want {
			item.Wanted, found = true, true
		}
		rows = append(rows, item)
		if len(rows) >= 25 {
			break
		}
	}
	c.render.fragment(w, "new", "releases", map[string]any{
		"Rows": rows, "AppID": appID,
		// WantedAny is what decides whether "serving now" still gets the
		// default. A copied release that is still on the list wins it, because
		// the person following that link asked for those bytes; with nothing
		// copied, or nothing found, the current release keeps it as before.
		"WantedAny": found,
		// Said out loud rather than passed over: somebody followed a link
		// offering to send the same release again and it is not on the list.
		"Gone": want != "" && !found,
		// Both said before the request is raised rather than discovered after:
		// a source app the policy has locked, and a newer build the policy
		// turned away, which is why it is not a row here.
		"Locked":  rep != nil && rep.LockedByPolicy,
		"Refused": refusedRelease(rep)})
}

// refusedRelease is the picker's sentence about a deploy the source's policy
// turned away, or nil when there is none.
func refusedRelease(rep *DependencyReport) map[string]string {
	if rep == nil || rep.Refused == nil {
		return nil
	}
	sha := rep.Refused.CommitSHA
	if len(sha) > 8 {
		sha = sha[:8]
	}
	return map[string]string{"SHA": sha, "Reason": rep.Refused.Reason}
}

func (c *Console) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form couldn’t be read. Reload the page and try again.", http.StatusBadRequest)
		return
	}

	appID := trimForm(r, "app_id")
	deployID := trimForm(r, "deployment_id")
	targetID := trimForm(r, "target_id")
	if appID == "" || deployID == "" || targetID == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "The request is incomplete",
			"Choose an app, a release and a target.")
		return
	}

	// The offer is the gate. Re-derived here rather than trusted from the form,
	// because a form field is not an authorization decision.
	apps, _, err := c.raisableApps(ctx, caller)
	if err != nil {
		c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t read the source install", err.Error())
		return
	}
	var app *App
	for i := range apps {
		if apps[i].ID == appID {
			app = &apps[i]
		}
	}
	if app == nil {
		// The refusal names the remedy, which is what the platform's own rule
		// asks of every refusal. An editor on somebody else's app cannot raise
		// a migration for it, and the copy says so rather than rendering an
		// empty list.
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t migrate that app",
			"You can migrate apps you own, directly or through a group, and apps you’ve been added to on a request. Ask the app’s owner to raise this one or add you to a request.")
		return
	}

	target, err := c.store.GetTarget(ctx, targetID)
	if err != nil || target == nil || target.Kind != TargetKindTarget {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That target doesn’t exist", "Choose one from the list.")
		return
	}

	// The install's own house rule, if it has set one. Checked here rather than
	// left to the reviewer, because the alternative is a request that comes
	// back a day later for a sentence its author has by then forgotten.
	settings, _ := c.store.Settings(ctx)
	if settings[SettingRequireNoteOnNew] == "true" && trimForm(r, "note") == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A note is required",
			"Add a note for the reviewer saying what’s in this release and what to watch after it ships.")
		return
	}

	source, _ := c.store.SourceTarget(ctx)
	sp, err := c.platformFor(source)
	if err != nil {
		c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t read the source install", err.Error())
		return
	}
	deploy, err := sp.Deployment(ctx, appID, deployID)
	if err != nil {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Couldn’t read that release", err.Error())
		return
	}
	// The picker offers only successful releases; a form built by hand is
	// held to the same rule here, rather than failing minutes after approval.
	if deploy.Status != "success" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That release didn’t build",
			"Only a release that built successfully can be migrated. Choose one from the list.")
		return
	}
	if vis := trimForm(r, "destination_visibility"); !validVisibility(vis) {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Choose a valid visibility",
			"Choose private, signed-in viewers or public.")
		return
	}

	// What it will be called on the far side. Defaulted from the link if one
	// exists — so a rename carries to the next request — and from the source
	// app's own slug if none does.
	link, _ := c.store.FindLink(ctx, targetID, appID)
	destSlug := sanitizeSlug(trimForm(r, "destination_slug"))
	if destSlug == "" {
		if link != nil && link.TargetAppSlug != "" {
			destSlug = link.TargetAppSlug
		} else {
			destSlug = app.Slug
		}
	}

	req := &Request{
		ID:                    newID(),
		Status:                StatusSubmitted,
		TargetID:              targetID,
		SourceAppID:           app.ID,
		SourceAppSlug:         app.Slug,
		SourceAppName:         app.Name,
		SourceDeploymentID:    deploy.ID,
		SourceReleaseLabel:    deploy.Label(),
		RequesterID:           caller.ID(),
		RequesterName:         caller.Name(),
		RequesterEmail:        caller.Email(),
		Reason:                trimForm(r, "reason"),
		Note:                  trimForm(r, "note"),
		DestinationSlug:       destSlug,
		DestinationVisibility: trimForm(r, "destination_visibility"),
		OwnerGroupID:          target.DefaultOwnerGroupID,
		OwnerGroupName:        target.DefaultOwnerGroupName,
		NotifyRequester:       r.FormValue("notify") == "on",
	}
	if link != nil {
		req.TargetAppID = link.TargetAppID
	}
	if err := c.store.InsertRequest(ctx, req); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the request", err.Error())
		return
	}
	_ = c.store.WriteEvent(ctx, Event{
		Action: "request.submitted", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: app.ID, TargetID: targetID,
		Detail: fmt.Sprintf("%s → %s, release %s, as %s",
			app.Name, target.Label, deploy.Label(), destSlug)})

	// The dependency verdict for the exact release being promoted, recorded on
	// the request at the moment it is raised. It is the one check kind with a
	// producer today, and it is worth more than a panel of placeholders.
	c.recordDependencyCheck(ctx, sp, req)

	c.redirect(w, r, "/requests/"+req.ID)
}

func (c *Console) recordDependencyCheck(ctx context.Context, sp *Platform, req *Request) {
	rep, err := sp.Dependencies(ctx, req.SourceAppID)
	if err != nil {
		// Absent, not failed, and never rendered as a pass: *not recorded* is
		// not *clean*.
		return
	}
	verdict, summary := rep.VerdictFor(req.SourceDeploymentID)
	if rep.LockedByPolicy {
		summary += "; Toft’s dependency policy has locked this app on the source"
	}
	now := time.Now().UTC()
	_ = c.store.SaveCheck(ctx, Check{
		ID: newID(), RequestID: req.ID, Kind: "dependencies", Tool: "Toft",
		Status: "recorded", Verdict: verdict, Summary: summary, RanAt: &now})
}
