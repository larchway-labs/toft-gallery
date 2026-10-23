package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// The four words a link can be in, and they are **derived at read time and
// never stored**.
//
// The console reads the source's deployment list with the read key and the
// target's with the target's key, and resolves one of four words — the
// Gallery's own rule for the same question, where *installed is derived, never
// stored*. Each state carries **when the console last looked**, because the
// answer moves only when it looks, and a freshness claim it cannot support is
// worse than a stale one it admits to.
// withAppsURL fills in where a target serves its apps when no test has
// recorded it yet — a target saved before the console asked. One short call,
// remembered; a target that does not answer simply gets no link this time.
func (c *Console) withAppsURL(ctx context.Context, t *Target) {
	if t == nil || t.Kind != TargetKindTarget || t.AppsURL != "" {
		return
	}
	p, err := c.platformFor(t)
	if err != nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if u, err := p.Reachable(callCtx); err == nil && u != "" {
		t.AppsURL = u
		_ = c.store.SetTargetAppsURL(ctx, t.ID, u)
	}
}

func (c *Console) linkStates(ctx context.Context, caller Caller) []LinkState {
	links, err := c.store.ListLinks(ctx)
	if err != nil {
		return nil
	}
	targets, _ := c.store.ListTargets(ctx)
	byID := map[string]Target{}
	for _, t := range targets {
		c.withAppsURL(ctx, &t)
		byID[t.ID] = t
	}
	source, _ := c.store.SourceTarget(ctx)
	var sp *Platform
	if source != nil {
		sp, _ = c.platformFor(source)
	}
	now := time.Now().UTC()

	out := make([]LinkState, 0, len(links))
	for _, l := range links {
		ls := LinkState{Link: l, State: LinkNotChecked, CheckedAt: now}
		t, ok := byID[l.TargetID]
		if ok {
			ls.TargetLabel = t.Label
			ls.AppURL = t.AppURL(l.TargetAppSlug)
		}
		if sp == nil || !ok {
			ls.Detail = "the console couldn’t reach one of the two installs on its last check"
			out = append(out, ls)
			continue
		}
		srcDeploys, err := sp.Deployments(ctx, l.SourceAppID)
		if err != nil {
			ls.Detail = "the source install didn’t answer: " + err.Error()
			out = append(out, ls)
			continue
		}
		newest := newestSuccess(srcDeploys)
		if newest == nil {
			ls.Detail = "the source app has no successful release"
			out = append(out, ls)
			continue
		}
		ls.SourceRelease = newest.Label()
		ls.NewestDeployID = newest.ID

		tp, err := c.platformFor(&t)
		if err != nil {
			ls.Detail = err.Error()
			out = append(out, ls)
			continue
		}
		tgtDeploys, err := tp.Deployments(ctx, l.TargetAppID)
		if err != nil {
			ls.Detail = "the target install didn’t answer: " + err.Error()
			out = append(out, ls)
			continue
		}
		current := newestSuccess(tgtDeploys)
		if current == nil {
			ls.Detail = "nothing is serving on the target yet"
			out = append(out, ls)
			continue
		}
		ls.TargetRelease = current.Label()

		switch {
		case current.ID != l.LastDeploymentID && l.LastDeploymentID != "":
			// Somebody deployed to the target directly. Surfaced and never
			// acted on: the target is running something this console did not
			// put there, and pushing over it needs a person who knows what that
			// something was.
			ls.State = LinkDiverged
			ls.Detail = "the release serving there isn’t the one this console deployed"
		case newest.ID != l.LastDeploymentID:
			ls.State = LinkBehind
			ls.Detail = "the source has been deployed since the last migration"
			ls.CanSendNewest = caller.Reviewer && t.FastTrack
		default:
			ls.State = LinkUpToDate
		}
		out = append(out, ls)
	}
	return out
}

func newestSuccess(ds []Deployment) *Deployment {
	for i := range ds {
		if ds[i].Status == "success" {
			return &ds[i]
		}
	}
	return nil
}

// handleLinks admits **an editor or a roster reviewer**, and the two see
// different pages.
//
// It used to be editors alone, and that made the one action on it unreachable
// by the only people allowed to take it. Fast-track needs `Reviewer`; the page
// carrying the button needed `Editor`; and the roster is deliberately not the
// administrators — "a Toft administrator is not automatically a reviewer"
// — so on a correctly configured install the intersection of the two is empty.
// The feature was testable only by breaking the rule it exists beside.
//
// So the gate is the union and the *controls* are split, which is the right
// shape anyway: reading which pairs are bound and which have moved on is
// exactly what a reviewer needs before deciding anything, and deleting a link
// is configuration. A reviewer sees the table and the fast-track button where a
// target has it switched on; an editor also sees Delete.
func (c *Console) handleLinks(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !caller.Editor && !caller.Reviewer {
		c.refuse(w, r, caller, http.StatusForbidden, "This page is for editors and reviewers",
			"Viewing links needs the owner or editor role on this app in Toft, or a place on the Reviewers page.")
		return
	}
	states := c.linkStates(r.Context(), caller)
	v := c.view(r, "Links", "links", caller, map[string]any{
		"Links": states, "Reviewer": caller.Reviewer, "Editor": caller.Editor})
	c.render.page(w, http.StatusOK, "links", v)
}

// handleDeleteLink: a link is deleted and remade, never re-pointed.
//
// A repointable link is a way to push app A's code over app B without anybody
// re-reading the request that authorised it. Deleting one moves nothing — the
// next request through the pair is a first deploy again, with the approver's
// choice back in play.
func (c *Console) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	id := r.PathValue("id")
	links, _ := c.store.ListLinks(r.Context())
	var found *Link
	for i := range links {
		if links[i].ID == id {
			found = &links[i]
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}
	// Type-to-confirm, on the source app's slug: nothing moves, but the next
	// request silently becomes a first deploy with the approver's choice back
	// in play, and that is worth a deliberate keystroke.
	if trimForm(r, "confirm") != found.SourceAppSlug {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That didn’t match",
			fmt.Sprintf("Type %s to confirm.", found.SourceAppSlug))
		return
	}
	_ = c.store.DeleteLink(r.Context(), id)
	_ = c.store.WriteEvent(r.Context(), Event{Action: "link.deleted",
		ActorID: caller.ID(), ActorName: caller.Name(),
		SourceAppID: found.SourceAppID, TargetID: found.TargetID,
		Detail: found.SourceAppSlug + " → " + found.TargetAppSlug})
	c.redirect(w, r, "/links")
}

// handleSendNewest is the fast-track, and it is narrowed three ways.
//
// The actor has to be a console reviewer *and* the target has to have it
// switched on. The self-approval rule is relaxed **here and nowhere else**, and
// what makes that defensible is:
//
//   - It is not a new destination. The link already exists, which means a first
//     deploy to that target app was reviewed and approved by somebody.
//   - It is not code the actor chose. It is what the source is serving right
//     now, and the pin is taken at the click rather than typed.
//   - It is not a new authority. This person is already a roster reviewer, so
//     they could have approved somebody else's request for these bytes to this
//     place. What the button removes is a step, not a permission.
//
// What it *does* collapse is the second pair of eyes, which is why the setting
// is per target and off by default, why the reason is required, and why the
// request is flagged `fast_track` in the ledger. **The record is the same
// whichever door was used**: who put release M on production has exactly one
// answer, and it is a request row.
func (c *Console) handleSendNewest(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil || !caller.Reviewer {
		c.refuse(w, r, caller, http.StatusForbidden, "Only reviewers can fast-track",
			"Ask someone on the Reviewers page, or raise a request instead.")
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()

	links, _ := c.store.ListLinks(ctx)
	var link *Link
	for i := range links {
		if links[i].ID == r.PathValue("id") {
			link = &links[i]
		}
	}
	if link == nil {
		http.NotFound(w, r)
		return
	}
	target, _ := c.store.GetTarget(ctx, link.TargetID)
	if target == nil || !target.FastTrack {
		c.refuse(w, r, caller, http.StatusForbidden, "Fast-track is off for this target",
			"Raise a request instead.")
		return
	}
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A fast-track needs a reason",
			"Say why this release is going without a second review.")
		return
	}
	if trimForm(r, "confirm") != target.Label {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That didn’t match",
			fmt.Sprintf("Type %s to confirm.", target.Label))
		return
	}

	source, _ := c.store.SourceTarget(ctx)
	sp, err := c.platformFor(source)
	if err != nil {
		c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t read this install", err.Error())
		return
	}
	deploys, err := sp.Deployments(ctx, link.SourceAppID)
	if err != nil {
		c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t read the source app", err.Error())
		return
	}
	newest := newestSuccess(deploys)
	if newest == nil {
		c.refuse(w, r, caller, http.StatusConflict, "There’s nothing to send",
			"The source app has no successful release.")
		return
	}

	// An ordinary request row: same fields, same ledger, same event history. A
	// second path that wrote a different kind of record is how an audit ends up
	// with two half-answers.
	now := time.Now().UTC()
	req := &Request{
		ID: newID(), Status: StatusApproved, TargetID: target.ID,
		SourceAppID: link.SourceAppID, SourceAppSlug: link.SourceAppSlug, SourceAppName: link.SourceAppName,
		SourceDeploymentID: newest.ID, SourceReleaseLabel: newest.Label(),
		RequesterID: caller.ID(), RequesterName: caller.Name(), RequesterEmail: caller.Email(),
		Reason: reason, DestinationSlug: link.TargetAppSlug, TargetAppID: link.TargetAppID,
		Via: "fast_track", NotifyRequester: false,
	}
	if err := c.store.InsertRequest(ctx, req); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the request", err.Error())
		return
	}
	// The row was inserted approved, so the tick may already have claimed it:
	// this records the decision without moving the status back under it.
	_ = c.store.UpdateRequestFields(ctx, req.ID, Event{
		Action: "request.fast_tracked", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: target.ID,
		Detail: fmt.Sprintf("fast-tracked without a second reviewer — %s", reason)},
		map[string]any{
			"decided_by_id": caller.ID(), "decided_by_name": caller.Name(),
			"decided_at": now, "decision_reason": reason})
	c.recordDependencyCheck(ctx, sp, req)
	c.redirect(w, r, "/requests/"+req.ID)
}
