package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RequestData is the approval screen: one column at reading width, evidence in
// reading order, and the decision at the **bottom**. A reviewer who can approve
// without scrolling past the documents is a reviewer the documents were
// pointless for.
type RequestData struct {
	Request
	Target             *Target
	Link               *Link
	Events             []Event
	Attachments        []Attachment
	AppAttach          []Attachment
	AppNote            *AppNote
	Viewers            []RequestViewer
	Checks             []Check
	Drift              string
	MayDecide          bool
	MayWithdraw        bool
	MayCancel          bool
	MayEdit            bool
	MayEditDestination bool
	MayAddPeople       bool
	FastTrack          bool
	Destination        string

	// TargetGroups is the group directory of the install this is going *to*,
	// read with that target's own key, and it is only ever populated on the
	// first migration of a pair — the one moment a destination is chosen.
	//
	// It replaces a text box that took a UUID typed by hand, and it is the
	// preflight as well as the picker: a target whose license locks Groups has
	// no groups to offer, and this is where that is said. Discovering it from
	// the push instead meant the failure arrived minutes later, on the far
	// side, reading "Service Owners has no member who could administer the app"
	// — a sentence about the wrong problem, at the wrong time, to the wrong
	// person.
	TargetGroups []Group
	// GroupsRefusal is why the list above is empty, when it is empty for a
	// reason worth reading. Absent is not off: a target that simply has no
	// groups and a target that could not be reached are different, and an
	// approver deciding between them needs to know which.
	GroupsRefusal string
	// People is the console's derived directory, offered as suggestions on the
	// "add somebody to this request" form. Never a gate — the field still takes
	// any id.
	People []Person

	// Upstream is the request this one promotes, UpstreamLabel its target,
	// and Kept the bundle this request's push kept for a later promotion.
	Upstream      *Request
	UpstreamLabel string
	Kept          *Bundle
	// Promotions is, on a deployed request whose target another requires,
	// each target it could go to next and whether it can go now.
	Promotions []PromotionOffer
	// MayResolveChecks is a reviewer who did not raise this request: who may
	// waive a lockfile check or acknowledge a parity difference.
	MayResolveChecks bool
}

// PromotionOffer is one "Promote to <label>" on a deployed request, or the
// sentence saying why it cannot go yet.
type PromotionOffer struct {
	Target Target
	OK     bool
	Why    string
}

// requestAudience answers who may open this request at all.
//
// The submitter, the people they added, every reviewer, and the console's
// editors. Anyone else gets the same 404 the platform gives for an object they
// may not see — a request names an app, a release and a person, and "you may
// not see this" is itself a disclosure on a change board.
func (c *Console) requestAudience(ctx context.Context, caller Caller, req *Request) (viewers []RequestViewer, mayOpen bool) {
	viewers, _ = c.store.ListRequestViewers(ctx, req.ID)
	if caller.Editor || caller.Reviewer || req.RequesterID == caller.ID() {
		return viewers, true
	}
	for _, v := range viewers {
		if v.UserID == caller.ID() {
			return viewers, true
		}
	}
	return viewers, false
}

func (c *Console) loadRequest(w http.ResponseWriter, r *http.Request, caller Caller) (*Request, []RequestViewer, bool) {
	if c.refuseUnidentified(w, r, caller) {
		return nil, nil, false
	}
	if caller.Viewer == nil {
		c.refuse(w, r, caller, http.StatusForbidden, "The console can’t tell who you are",
			"Viewer identity is off for this app, so requests can’t be shown. A Toft administrator can turn it on.")
		return nil, nil, false
	}
	req, err := c.store.GetRequest(r.Context(), r.PathValue("id"))
	if err != nil || req == nil {
		c.refuse(w, r, caller, http.StatusNotFound, "Request not found", "It may have been raised on another install, or the link may be out of date.")
		return nil, nil, false
	}
	viewers, ok := c.requestAudience(r.Context(), caller, req)
	if !ok {
		c.refuse(w, r, caller, http.StatusNotFound, "Request not found",
			"Only the requester, people added to the request and reviewers can see it.")
		return nil, nil, false
	}
	return req, viewers, true
}

func (c *Console) handleRequest(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, viewers, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	ctx := r.Context()

	data := RequestData{Request: *req, Viewers: viewers}
	data.Target, _ = c.store.GetTarget(ctx, req.TargetID)
	if req.Status == StatusDeployed {
		c.withAppsURL(ctx, data.Target)
	}
	data.Link, _ = c.store.FindLink(ctx, req.TargetID, req.SourceAppID)
	data.Events, _ = c.store.RequestEvents(ctx, req.ID)
	data.Attachments, _ = c.store.RequestAttachments(ctx, req.ID)
	data.AppAttach, _ = c.store.AppAttachments(ctx, req.SourceAppID)
	data.AppNote, _ = c.store.AppNote(ctx, req.SourceAppID)
	data.Checks, _ = c.store.Checks(ctx, req.ID)

	// **A reviewer may not approve their own request.** One comparison, and it
	// is the feature.
	data.MayDecide = caller.Reviewer && req.Status == StatusSubmitted && req.RequesterID != caller.ID()
	data.MayWithdraw = req.RequesterID == caller.ID() && req.Status == StatusSubmitted
	// The note, the destination slug and the attachments stay editable for both
	// sides while the request can still move. Requester *and* approver, because
	// the thing a change board most often needs is for the approver to correct
	// the paperwork rather than reject and make somebody start again.
	data.MayEdit = !Terminal(req.Status) &&
		(req.RequesterID == caller.ID() || caller.Reviewer || caller.Editor || isViewer(viewers, caller.ID()))
	// The destination is narrower: once a reviewer has decided, the name and
	// the audience are what they approved, and changing either would ship
	// something nobody reviewed.
	data.MayEditDestination = data.MayEdit && req.Status == StatusSubmitted
	data.MayAddPeople = !Terminal(req.Status) && (req.RequesterID == caller.ID() || caller.Editor)
	// **Cancelling is a different act from withdrawing, and a different one
	// from dropping a schedule.** It undoes a decision somebody recorded, so it
	// is open to the roster — and to the requester, because the person who
	// asked for a change is usually the first to know it should not go, and
	// making them find a reviewer during a release window is how a production
	// push happens that everybody wanted to stop.
	data.MayCancel = req.Status == StatusApproved && (caller.Reviewer || req.RequesterID == caller.ID())
	if data.Target != nil {
		data.FastTrack = data.Target.FastTrack
	}
	if data.MayDecide && data.Link == nil && (data.Target == nil || data.Target.DefaultOwnerGroupID == "") {
		data.TargetGroups, data.GroupsRefusal = c.targetGroups(ctx, data.Target)
	}
	if data.MayAddPeople {
		data.People, _ = c.store.KnownPeople(ctx)
	}
	data.Destination = c.describeDestination(req, data.Link)

	data.MayResolveChecks = caller.Reviewer && req.RequesterID != caller.ID()
	data.Kept, _ = c.store.BundleInfo(ctx, req.ID)
	if req.PromotedFrom != "" {
		data.Upstream, _ = c.store.GetRequest(ctx, req.PromotedFrom)
		if data.Upstream != nil {
			if t, _ := c.store.GetTarget(ctx, data.Upstream.TargetID); t != nil {
				data.UpstreamLabel = t.Label
			}
		}
	}
	if req.Status == StatusDeployed {
		data.Promotions = c.promotionOffers(ctx, caller, req, data.Target)
	}

	v := c.view(r, "Request", "queue", caller, data)
	c.render.page(w, http.StatusOK, "request", v)
}

// promotionOffers is the Promote action on a deployed request: one per target
// that takes releases from this one's, for somebody who may raise for the app.
func (c *Console) promotionOffers(ctx context.Context, caller Caller, req *Request, target *Target) []PromotionOffer {
	gated, _ := c.store.TargetsRequiring(ctx, req.TargetID)
	if len(gated) == 0 || target == nil || caller.Viewer == nil {
		return nil
	}
	upstream, ok, why := c.promotionFor(ctx, caller, req.ID)
	if upstream == nil {
		return nil
	}
	may := map[string]bool{}
	for _, t := range ok {
		may[t.ID] = true
	}
	var out []PromotionOffer
	for _, t := range gated {
		o := PromotionOffer{Target: t, OK: may[t.ID], Why: why}
		if !o.OK && why == "" {
			_, o.Why = promotable(ctx, c.reads(), req, target.Label, &t)
		}
		out = append(out, o)
	}
	return out
}

func isViewer(vs []RequestViewer, id string) bool {
	for _, v := range vs {
		if v.UserID == id {
			return true
		}
	}
	return false
}

// targetGroups reads the group directory of the install a request is going to,
// and says why it could not when it could not.
//
// Both deploy-key scopes may read GET /groups and GET /groups/{id} — names,
// counts and addresses, never a roster — which is exactly enough to turn
// `owner_group_id` from a typed UUID into a picker, on the far side as well as
// this one. Nothing here is authorization: the approver still chooses, and the
// push still has to be accepted by the target.
//
// The refusal string carries the *reason*, because the three empties are
// different questions. A target that cannot be reached is an operations
// problem; a target that answers with no groups at all is almost always a fresh
// install on the demo tier, where Groups are locked — and that is worth naming,
// because the console requires a group for an app it creates and the person
// reading this cannot fix a license from here.
func (c *Console) targetGroups(ctx context.Context, t *Target) ([]Group, string) {
	if t == nil {
		return nil, "The target for this request has been removed."
	}
	p, err := c.platformFor(t)
	if err != nil {
		return nil, "Couldn’t read the stored key for " + t.Label + ": " + err.Error()
	}
	groups, err := p.Groups(ctx)
	if err != nil {
		return nil, "Couldn’t read the groups on " + t.Label + ": " + err.Error() +
			" Fix this before approving, or the migration will fail."
	}
	if len(groups) == 0 {
		return nil, t.Label + " has no groups, and a new app there needs a group to own it. " +
			"An administrator on " + t.Label + " needs to create a group, or apply a license " +
			"if groups are locked, before this can be approved."
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups, ""
}

// describeDestination is the sentence shown above the decision — the same one
// the submitter chose from, repeated verbatim so the approver reads what was
// chosen rather than a paraphrase of it.
func (c *Console) describeDestination(req *Request, link *Link) string {
	slug := req.DestinationSlug
	if slug == "" {
		slug = req.SourceAppSlug
	}
	if link != nil {
		return fmt.Sprintf("the existing app %s, which this console last deployed to", link.TargetAppSlug)
	}
	if req.OwnerGroupName != "" {
		return fmt.Sprintf("a new app %s, owned by %s", slug, req.OwnerGroupName)
	}
	return fmt.Sprintf("a new app %s; the approver chooses its owning group", slug)
}

// --- Decisions --------------------------------------------------------------

func (c *Console) handleApprove(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	ctx := r.Context()
	if !caller.Reviewer {
		c.refuse(w, r, caller, http.StatusForbidden, "You’re not a reviewer",
			"Only people on this console’s Reviewers page can approve requests. Toft administrators aren’t reviewers unless they’re added there.")
		return
	}
	if req.RequesterID == caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t approve your own request",
			"Ask another reviewer. If fast-track is allowed for this target, you can send the current release from the Links page instead.")
		return
	}
	if req.Status != StatusSubmitted {
		c.refuse(w, r, caller, http.StatusConflict, "This request has already been decided",
			"To try again, raise a new request.")
		return
	}
	target, _ := c.store.GetTarget(ctx, req.TargetID)
	if target == nil {
		c.refuse(w, r, caller, http.StatusConflict, "The target has been removed",
			"This request’s target was removed from the console, so nothing can be sent to it.")
		return
	}
	// A promotion is approved only while it could still run, and only once a
	// lockfile check the target asks for has passed or been waived. The push
	// asks both again; this saves a reviewer approving something that cannot.
	if req.PromotedFrom != "" {
		upstream, _ := c.store.GetRequest(ctx, req.PromotedFrom)
		if ok, why := c.promotableNow(ctx, upstream, target); !ok {
			c.refuse(w, r, caller, http.StatusConflict, "This can’t be promoted now", why)
			return
		}
		if target.RequiresLockfile {
			checks, _ := c.store.Checks(ctx, req.ID)
			if lc := CheckOf(checks, CheckLockfile); lc == nil || (lc.Status != CheckPass && lc.Status != CheckWaived) {
				c.refuse(w, r, caller, http.StatusConflict, "The lockfile check hasn’t passed",
					target.Label+" requires locked dependencies. A reviewer who didn’t raise this request can waive the check, with a reason, on this page.")
				return
			}
		}
	}
	_ = r.ParseForm()

	fields := map[string]any{
		"decided_by_id":   caller.ID(),
		"decided_by_name": caller.Name(),
		"decided_at":      time.Now().UTC(),
		"decision_reason": trimForm(r, "reason"),
	}
	// The approver is the only person who ever chooses the destination, and
	// they choose it once — at the first deploy of a (target, source app) pair.
	link, _ := c.store.FindLink(ctx, req.TargetID, req.SourceAppID)
	if link == nil {
		group := trimForm(r, "owner_group_id")
		// **A target that names an owning group owns every app created on it.**
		// It is the connection's setting, not the approver's choice: Stage's
		// apps belong to Stage's group and Prod's to Prod's, whoever approves.
		// Read from the target now, not from the copy on the request, so a
		// change on the Targets page applies to requests already raised. It is
		// checked against the target's directory below like any other.
		if target.DefaultOwnerGroupID != "" {
			group = target.DefaultOwnerGroupID
		}
		if group == "" {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Choose an owning group",
				"This is the app’s first migration to this target, so it will be created there and needs a group to own it. Choose one from the list on the request.")
			return
		}
		// **Decide, then confirm — before the push rather than during it.**
		//
		// The group is checked against the target's own directory here, at the
		// moment somebody chooses it, because the alternative is what this cost
		// the first time: the approval succeeded, the schedule fired, the
		// bundle went over, and the far side refused it with a sentence about a
		// group having no eligible member. That failure is minutes late, it is
		// on a page the approver has stopped looking at, and it is worded as a
		// problem with the app rather than with the choice they just made.
		//
		// Three things are checked and the name is *taken from the directory*
		// rather than from the form: a label the approver could type freely is
		// a label that can disagree with the id beside it, and the ledger keeps
		// both for ever.
		groups, refusal := c.targetGroups(ctx, target)
		if refusal != "" {
			c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t check the target", refusal)
			return
		}
		var chosen *Group
		for i := range groups {
			if groups[i].ID == group {
				chosen = &groups[i]
			}
		}
		if chosen == nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group isn’t on the target",
				fmt.Sprintf("%s has no group with that id. Choose one from the list on the request.", target.Label))
			return
		}
		if chosen.Members == 0 {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group has no members",
				fmt.Sprintf("%s on %s has no members, so it can’t own an app. Choose another group, or ask an administrator there to add someone to it.", chosen.Name, target.Label))
			return
		}
		fields["owner_group_id"] = chosen.ID
		fields["owner_group_name"] = chosen.Name
	}
	if slug := sanitizeSlug(trimForm(r, "destination_slug")); slug != "" {
		fields["destination_slug"] = slug
	}
	if vis := trimForm(r, "destination_visibility"); vis != "" {
		if !validVisibility(vis) {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Choose a valid visibility",
				"Choose private, signed-in viewers or public.")
			return
		}
		fields["destination_visibility"] = vis
	}

	detail := "approved"
	if when := trimForm(r, "scheduled_for"); when != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", when, scheduleZone(r))
		if err != nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That isn’t a valid time", "Use the date and time field.")
			return
		}
		fields["scheduled_for"] = t.UTC()
		detail = "approved, scheduled for " + inZone(t, "2 Jan 2006 15:04 MST")
	}

	// Conditional on the row still being `submitted`. The checks above can
	// take a network round trip to the target, and a withdrawal or another
	// reviewer's rejection that lands meanwhile must stand: an approval
	// written over either would ship something its requester called off.
	moved, err := c.store.SetStatusIf(ctx, req.ID, StatusSubmitted, StatusApproved, Event{
		Action: "request.approved", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: detail}, fields)
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the approval", err.Error())
		return
	}
	if !moved {
		c.refuse(w, r, caller, http.StatusConflict, "This request has already been decided",
			"It was withdrawn or decided by another reviewer while you were approving it.")
		return
	}
	if _, scheduled := fields["scheduled_for"]; !scheduled {
		c.tickSoon(r)
	}
	c.redirect(w, r, "/requests/"+req.ID)
}

// handleWaiveCheck moves a failing lockfile check to waived, and
// handleAcknowledgeCheck a parity difference to acknowledged. Two routes rather
// than one with an action field, because each accepts exactly one check kind in
// one status, and anything else is refused with a sentence.
func (c *Console) handleWaiveCheck(w http.ResponseWriter, r *http.Request) {
	c.resolveCheck(w, r, CheckLockfile, CheckFail, CheckWaived, "check.waived", "waive",
		"Only a failing lockfile check can be waived.")
}

func (c *Console) handleAcknowledgeCheck(w http.ResponseWriter, r *http.Request) {
	c.resolveCheck(w, r, CheckParity, CheckDiffers, CheckAcknowledged, "parity.acknowledged", "acknowledge",
		"Only a build parity check that found a difference can be acknowledged.")
}

// resolveCheck is the shared half: a reviewer who did not raise the request,
// a reason, and a move that is conditional on the check still reading `from`.
func (c *Console) resolveCheck(w http.ResponseWriter, r *http.Request, kind, from, to, action, verb, wrong string) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	ctx := r.Context()
	if !caller.Reviewer {
		c.refuse(w, r, caller, http.StatusForbidden, "You’re not a reviewer",
			"Only people on this console’s Reviewers page can "+verb+" a check.")
		return
	}
	if req.RequesterID == caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t "+verb+" a check on your own request",
			"Ask another reviewer.")
		return
	}
	checks, _ := c.store.Checks(ctx, req.ID)
	var check *Check
	for i := range checks {
		if checks[i].ID == r.PathValue("checkID") {
			check = &checks[i]
		}
	}
	if check == nil {
		c.refuse(w, r, caller, http.StatusNotFound, "Check not found", "It isn’t on this request.")
		return
	}
	if check.Kind != kind || check.Status != from {
		c.refuse(w, r, caller, http.StatusConflict, "That check can’t be changed", wrong)
		return
	}
	if kind == CheckLockfile && Terminal(req.Status) {
		c.refuse(w, r, caller, http.StatusConflict, "This request has ended",
			"A waiver only matters before a request is pushed. Raise the promotion again.")
		return
	}
	_ = r.ParseForm()
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A reason is required",
			"Say why this is acceptable. The reason is kept on the check and in the history.")
		return
	}
	moved, err := c.store.UpdateCheckStatus(ctx, check.ID, from, to,
		fmt.Sprintf("%s by %s: %s", statusLabel(to), caller.Name(), reason),
		Event{Action: action, ActorID: caller.ID(), ActorName: caller.Name(),
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID, Detail: reason})
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save that", err.Error())
		return
	}
	if !moved {
		c.refuse(w, r, caller, http.StatusConflict, "That check has already changed",
			"Another reviewer got there first. Reload the request.")
		return
	}
	c.redirect(w, r, "/requests/"+req.ID)
}

// tickSoon starts the tick now, in the name of the person who made a request
// due, so "approve" means "go" rather than "go within five minutes". Best
// effort: a start that is refused leaves the request approved and the schedule
// picks it up, and a refusal for jobs being off is recorded for /health.
func (c *Console) tickSoon(r *http.Request) {
	if err := c.startTick(r.Context(), r); err != nil {
		log.Printf("starting the tick now: %v — the schedule will pick the request up", err)
	}
}

func (c *Console) handleReject(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if !caller.Reviewer || req.Status != StatusSubmitted || req.RequesterID == caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t reject this request",
			"Only a reviewer who didn’t raise the request can reject it, while it’s awaiting review.")
		return
	}
	_ = r.ParseForm()
	// A rejection the ledger cannot explain is not a record.
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A rejection needs a reason",
			"Tell the requester what to change.")
		return
	}
	moved, err := c.store.SetStatusIf(r.Context(), req.ID, StatusSubmitted, StatusRejected, Event{
		Action: "request.rejected", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: reason}, map[string]any{
		"decided_by_id": caller.ID(), "decided_by_name": caller.Name(),
		"decided_at": time.Now().UTC(), "decision_reason": reason})
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the rejection", err.Error())
		return
	}
	if !moved {
		c.refuse(w, r, caller, http.StatusConflict, "This request has already been decided",
			"It was withdrawn or decided by another reviewer while you were rejecting it.")
		return
	}
	targetName := req.TargetID
	if t, _ := c.store.GetTarget(r.Context(), req.TargetID); t != nil {
		targetName = t.Label
	}
	c.notifyRequester(r.Context(), req, "Your migration request was rejected",
		fmt.Sprintf("%s rejected the request to move %s to %s.\n\nReason: %s",
			caller.Name(), req.SourceAppName, targetName, reason))
	c.redirect(w, r, "/requests/"+req.ID)
}

func (c *Console) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if req.RequesterID != caller.ID() || req.Status != StatusSubmitted {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t withdraw this request",
			"Only the person who raised a request can withdraw it, and only before it’s decided.")
		return
	}
	// Conditional for the reason approval is: a decision that landed first stands.
	_, _ = c.store.SetStatusIf(r.Context(), req.ID, StatusSubmitted, StatusWithdrawn, Event{
		Action: "request.withdrawn", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID}, nil)
	c.redirect(w, r, "/requests/"+req.ID)
}

// handleCancel stops an approved migration before it starts.
//
// **It is a state, not a copy change**, and that is the whole of the argument
// for building it rather than wording around it. Until now the only two levers
// on an approved request were withdraw — the requester's, and only before a
// decision — and unschedule, which does the opposite of what somebody reaching
// for it in a release window intends: it makes the push *due*. Once approved, a
// migration went. A change board that cannot call one off is one people route
// around, and the routing-around is somebody revoking a deploy key at 5pm.
//
// Three things make it safe to have:
//
//   - **It only ever un-approves; it never un-decides anything else.** The one
//     legal move is approved → cancelled. A rejection is still terminal, a
//     deployed release is still deployed, and a push already in flight refuses
//     with 409 rather than pretending.
//   - **It races the tick honestly.** The claim is conditional on both sides
//     (see Store.SetStatusIf): the tick may be part-way through downloading a
//     release archive when this lands, and whichever write gets the row first
//     is the one that stands. There is no window in which a request reads
//     cancelled and a bundle is on its way anyway.
//   - **It is not a retry lever.** Cancelling is terminal like every other
//     ending. The bytes were approved once; wanting them again is a new
//     request, which is the same rule rejection has always had.
//
// A reason is required, for the reason a rejection's is: a year later the
// ledger is the only thing left, and "cancelled" with no sentence beside it is
// a record of nothing.
func (c *Console) handleCancel(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	ctx := r.Context()
	if !caller.Reviewer && req.RequesterID != caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t cancel this migration",
			"Only reviewers and the person who raised the request can cancel an approved migration.")
		return
	}
	switch req.Status {
	case StatusApproved:
	case StatusDeploying:
		c.refuse(w, r, caller, http.StatusConflict, "This migration has already started",
			"It’s building on the target and can’t be recalled. Any rollback has to be done on that install.")
		return
	default:
		c.refuse(w, r, caller, http.StatusConflict, "There’s nothing to cancel",
			"Only an approved migration that hasn’t started can be cancelled. This one is "+statusLabel(req.Status)+".")
		return
	}
	_ = r.ParseForm()
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Cancelling needs a reason",
			"Say why this approved migration is being cancelled.")
		return
	}

	moved, err := c.store.SetStatusIf(ctx, req.ID, StatusApproved, StatusCancelled, Event{
		Action: "request.cancelled", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: reason}, map[string]any{"decision_reason": reason})
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the cancellation", err.Error())
		return
	}
	if !moved {
		// The tick got there first. Said plainly rather than retried: the push
		// has begun, and the honest answer is the same 409 as above.
		c.refuse(w, r, caller, http.StatusConflict, "The migration started while you were cancelling",
			"Nothing was cancelled. The request page shows its progress.")
		return
	}
	c.notifyRequester(ctx, req, "Your migration was cancelled",
		fmt.Sprintf("%s cancelled the approved migration of %s before it started.\n\nReason: %s",
			caller.Name(), req.SourceAppName, reason))
	c.redirect(w, r, "/requests/"+req.ID)
}

func (c *Console) handleUnschedule(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if !caller.Reviewer && req.RequesterID != caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You can’t change this schedule", "Only a reviewer or the requester can.")
		return
	}
	if req.Status != StatusApproved {
		c.refuse(w, r, caller, http.StatusConflict, "Nothing is scheduled", "Only an approved request can have a schedule.")
		return
	}
	_ = c.store.UpdateRequestFields(r.Context(), req.ID, Event{
		Action: "request.unscheduled", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID},
		map[string]any{"scheduled_for": nil})
	c.tickSoon(r)
	c.redirect(w, r, "/requests/"+req.ID)
}

// --- The note, the destination, and the people on a request ----------------

// handleRequestNote writes the request-level note. Requester, approver and
// anybody named on the request may edit it while the request can still move:
// the point of a note is that the people looking at one decision can write down
// what they know, and a field only one of them may fill in is a field that gets
// worked around in a chat window.
func (c *Console) handleRequestNote(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, viewers, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if Terminal(req.Status) {
		http.Error(w, "This request has been decided, so its note can’t be changed.", http.StatusConflict)
		return
	}
	if !(req.RequesterID == caller.ID() || caller.Reviewer || caller.Editor || isViewer(viewers, caller.ID())) {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	note := trimForm(r, "note")
	if err := c.store.UpdateRequestFields(r.Context(), req.ID, Event{
		Action: "request.note_edited", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID,
		Detail: firstLine(note)}, map[string]any{"note": note}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Note = note
	c.render.fragment(w, "request", "note", RequestData{Request: *req, MayEdit: true, Viewers: viewers})
}

// handleDestination changes what the app will be called on the far side, and
// who will be able to see it.
//
// Both the requester and the approver may change it, and the change carries to
// the next migration through the same link, because it is a property of the
// destination rather than of one push.
func (c *Console) handleDestination(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, viewers, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	// Fixed once decided: the approver chose a name and an audience, and a
	// change after that would ship something nobody reviewed.
	if req.Status != StatusSubmitted {
		http.Error(w, "The destination can’t be changed after the request is decided.", http.StatusConflict)
		return
	}
	if !(req.RequesterID == caller.ID() || caller.Reviewer || caller.Editor) {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	slug := sanitizeSlug(trimForm(r, "destination_slug"))
	if slug == "" {
		http.Error(w, "Enter the app’s name on the target.", http.StatusUnprocessableEntity)
		return
	}
	vis := trimForm(r, "destination_visibility")
	if !validVisibility(vis) {
		http.Error(w, "Choose private, signed-in viewers or public.", http.StatusUnprocessableEntity)
		return
	}
	if err := c.store.UpdateRequestFields(r.Context(), req.ID, Event{
		Action: "request.destination_changed", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: fmt.Sprintf("as %s, %s", slug, visibilityWord(vis))},
		map[string]any{"destination_slug": slug, "destination_visibility": vis}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.DestinationSlug, req.DestinationVisibility = slug, vis
	link, _ := c.store.FindLink(r.Context(), req.TargetID, req.SourceAppID)
	c.render.fragment(w, "request", "destination", RequestData{
		Request: *req, Link: link, Viewers: viewers, MayEdit: true, MayEditDestination: true,
		Destination: c.describeDestination(req, link)})
}

func visibilityWord(v string) string {
	switch v {
	case "public":
		return "public"
	case "authenticated":
		return "signed-in viewers only"
	case "private":
		return "private"
	}
	return "visibility unchanged"
}

// handleAddViewer names somebody else on this request.
//
// They read it, they watch it, and — because they were trusted with this one —
// they may raise the next request for the same source app. That last part is
// the reason this is not simply a mailing list: a migration is usually a
// team's, and a queue only its raiser can open is a queue somebody re-asks for
// in a chat window.
func (c *Console) handleAddViewer(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, viewers, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if Terminal(req.Status) {
		http.Error(w, "This request has already been decided.", http.StatusConflict)
		return
	}
	if req.RequesterID != caller.ID() && !caller.Editor {
		http.Error(w, "Only the person who raised a request can add people to it.", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	id := trimForm(r, "user_id")
	name := trimForm(r, "user_name")
	if id == "" {
		http.Error(w, "Enter a Toft user id.", http.StatusUnprocessableEntity)
		return
	}
	if name == "" {
		name = id
	}
	if err := c.store.AddRequestViewer(r.Context(), RequestViewer{
		RequestID: req.ID, UserID: id, UserName: name,
		UserEmail: trimForm(r, "user_email"), AddedByName: caller.Name()},
		Event{Action: "request.person_added", ActorID: caller.ID(), ActorName: caller.Name(),
			RequestID: req.ID, SourceAppID: req.SourceAppID, Detail: name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	viewers, _ = c.store.ListRequestViewers(r.Context(), req.ID)
	people, _ := c.store.KnownPeople(r.Context())
	c.render.fragment(w, "request", "people", RequestData{
		Request: *req, Viewers: viewers, MayAddPeople: true, People: people})
}

func (c *Console) handleRemoveViewer(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if req.RequesterID != caller.ID() && !caller.Editor {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	userID := r.PathValue("userID")
	if err := c.store.RemoveRequestViewer(r.Context(), req.ID, userID, Event{
		Action: "request.person_removed", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, Detail: userID}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	viewers, _ := c.store.ListRequestViewers(r.Context(), req.ID)
	people, _ := c.store.KnownPeople(r.Context())
	c.render.fragment(w, "request", "people", RequestData{
		Request: *req, Viewers: viewers, MayAddPeople: true, People: people})
}

// handleRequestProgress is the *deploying* region, re-read on a timer.
//
// It names the target's own deployment and says **when it was last checked**,
// because the console learns on a tick and pretending to be live is the one
// thing the deploy rule forbids: progress is a stream of snapshots and never
// load-bearing.
func (c *Console) handleRequestProgress(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	data := RequestData{Request: *req}
	data.Target, _ = c.store.GetTarget(r.Context(), req.TargetID)
	if req.Status == StatusDeployed {
		c.withAppsURL(r.Context(), data.Target)
	}
	c.render.fragment(w, "request", "progress", data)
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

var _ = context.Background

func validVisibility(v string) bool {
	switch v {
	case "", "private", "authenticated", "public":
		return true
	}
	return false
}

// scheduleZone is the zone a `datetime-local` value was typed in. The field
// carries no offset, so the form adds the browser's (`tz_offset`, minutes east
// of UTC at the chosen moment); without it the value is read on the process
// clock, which in a Toft workload is UTC — a reviewer at UTC-5 scheduling
// 22:00 would otherwise ship at 17:00 their time.
func scheduleZone(r *http.Request) *time.Location {
	// A zone chosen on Settings is the clock every time is shown in, so a
	// typed time is read on it too — the field and the page then agree.
	if zoneSet() {
		return zone()
	}
	if v := trimForm(r, "tz_offset"); v != "" {
		if mins, err := strconv.Atoi(v); err == nil && mins >= -14*60 && mins <= 14*60 {
			return time.FixedZone("browser", mins*60)
		}
	}
	return time.Local
}
