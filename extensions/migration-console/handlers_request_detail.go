package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// RequestData is the approval screen: one column at reading width, evidence in
// reading order, and the decision at the **bottom**. A reviewer who can approve
// without scrolling past the documents is a reviewer the documents were
// pointless for.
type RequestData struct {
	Request
	Target       *Target
	Link         *Link
	Events       []Event
	Attachments  []Attachment
	AppAttach    []Attachment
	AppNote      *AppNote
	Viewers      []RequestViewer
	Checks       []Check
	Drift        string
	MayDecide    bool
	MayWithdraw  bool
	MayCancel    bool
	MayEdit      bool
	MayAddPeople bool
	FastTrack    bool
	Destination  string

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
	if caller.Viewer == nil {
		c.refuse(w, r, caller, http.StatusForbidden, "The console cannot tell who you are",
			"Viewer identity is switched off for this app, so it cannot decide whether this request is yours to read. A Toft administrator can turn it on.")
		return nil, nil, false
	}
	req, err := c.store.GetRequest(r.Context(), r.PathValue("id"))
	if err != nil || req == nil {
		c.refuse(w, r, caller, http.StatusNotFound, "No such request", "It may have been raised on another install, or the link may be stale.")
		return nil, nil, false
	}
	viewers, ok := c.requestAudience(r.Context(), caller, req)
	if !ok {
		c.refuse(w, r, caller, http.StatusNotFound, "No such request",
			"You are not the requester, you have not been added to this one, and you are not on the reviewer roster.")
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
	if data.MayDecide && data.Link == nil {
		data.TargetGroups, data.GroupsRefusal = c.targetGroups(ctx, data.Target)
	}
	if data.MayAddPeople {
		data.People, _ = c.store.KnownPeople(ctx)
	}
	data.Destination = c.describeDestination(req, data.Link)

	v := c.view(r, "Request", "queue", caller, data)
	c.render.page(w, http.StatusOK, "request", v)
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
		return nil, "The target this request was raised against is gone."
	}
	p, err := c.platformFor(t)
	if err != nil {
		return nil, "The stored key for " + t.Label + " could not be read: " + err.Error()
	}
	groups, err := p.Groups(ctx)
	if err != nil {
		return nil, "The group directory on " + t.Label + " could not be read: " + err.Error() +
			" Approving anyway will fail at the push, so it is worth fixing first."
	}
	if len(groups) == 0 {
		return nil, t.Label + " has no groups. A deploy key belongs to nobody, so an app created " +
			"there needs one to own it — and a Toft install with no groups at all is usually " +
			"one whose license tier locks them. Somebody with an administrator account on " +
			t.Label + " has to create a group, or apply a license, before this can be approved."
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
	return fmt.Sprintf("a new app %s — the approver has to say which group will own it", slug)
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
		c.refuse(w, r, caller, http.StatusForbidden, "You are not on the reviewer roster",
			"Being a Toft administrator makes you an editor of this app’s configuration; it does not make you an approver. The roster is this app’s own authority.")
		return
	}
	if req.RequesterID == caller.ID() {
		c.refuse(w, r, caller, http.StatusForbidden, "You raised this one",
			"A reviewer may not approve their own request. Ask another reviewer, or — where an administrator has switched fast-track on for this target — send an established link’s current release from the Links page, which is flagged as a fast-track and needs a reason.")
		return
	}
	if req.Status != StatusSubmitted {
		c.refuse(w, r, caller, http.StatusConflict, "This request has already been decided",
			"Its history is on the request. A rejected request is never reopened — raise a new one.")
		return
	}
	target, _ := c.store.GetTarget(ctx, req.TargetID)
	if target == nil {
		c.refuse(w, r, caller, http.StatusConflict, "The target is gone",
			"This request names a target install that has since been removed from the console. Its record stays; nothing can be sent to it.")
		return
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
		if group == "" {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "This one needs an owning group",
				"Nothing has been migrated from this app to this target before, so the app will be created there — and a deploy key belongs to nobody, so a group has to own it. Pick one from the list on the request.")
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
			c.refuse(w, r, caller, http.StatusBadGateway, "The target could not be checked", refusal)
			return
		}
		var chosen *Group
		for i := range groups {
			if groups[i].ID == group {
				chosen = &groups[i]
			}
		}
		if chosen == nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That is not a group on the target",
				fmt.Sprintf("%s does not have a group with that id. Pick one from the list on the request — it is read from that install, with the key stored for it.", target.Label))
			return
		}
		if chosen.Members == 0 {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group has nobody in it",
				fmt.Sprintf("%s exists on %s and has no members, so the app would be created owned by nobody — which the target refuses at the push, several minutes from now, in words about the app rather than about this choice. Somebody with an administrator account there has to put a person in it first.", chosen.Name, target.Label))
			return
		}
		fields["owner_group_id"] = chosen.ID
		fields["owner_group_name"] = chosen.Name
	}
	if slug := trimForm(r, "destination_slug"); slug != "" {
		fields["destination_slug"] = slug
	}
	if vis := trimForm(r, "destination_visibility"); vis != "" {
		fields["destination_visibility"] = vis
	}

	detail := "approved"
	if when := trimForm(r, "scheduled_for"); when != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", when, time.Local)
		if err != nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That is not a time", "Use the date and time field.")
			return
		}
		fields["scheduled_for"] = t.UTC()
		detail = "approved, scheduled for " + t.Format("2 Jan 2006 15:04")
	}

	if err := c.store.SetStatus(ctx, req.ID, StatusApproved, Event{
		Action: "request.approved", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: detail}, fields); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "The approval could not be saved", err.Error())
		return
	}
	if _, scheduled := fields["scheduled_for"]; !scheduled {
		c.tickSoon(r)
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
		c.refuse(w, r, caller, http.StatusForbidden, "This is not yours to decide",
			"Rejecting needs a reviewer who did not raise the request, on a request still awaiting review.")
		return
	}
	_ = r.ParseForm()
	// A rejection the ledger cannot explain is not a record.
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A rejection needs a reason",
			"It is the only thing the requester has to work from, and a year later it is the only thing the record has.")
		return
	}
	err := c.store.SetStatus(r.Context(), req.ID, StatusRejected, Event{
		Action: "request.rejected", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: reason}, map[string]any{
		"decided_by_id": caller.ID(), "decided_by_name": caller.Name(),
		"decided_at": time.Now().UTC(), "decision_reason": reason})
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "The rejection could not be saved", err.Error())
		return
	}
	c.notifyRequester(r.Context(), req, "Your migration request was rejected",
		fmt.Sprintf("%s rejected the request to move %s to %s.\n\nReason: %s",
			caller.Name(), req.SourceAppName, req.TargetID, reason))
	c.redirect(w, r, "/requests/"+req.ID)
}

func (c *Console) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, _, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	if req.RequesterID != caller.ID() || req.Status != StatusSubmitted {
		c.refuse(w, r, caller, http.StatusForbidden, "This is not yours to withdraw",
			"Only the person who raised a request can withdraw it, and only before it has been decided.")
		return
	}
	_ = c.store.SetStatus(r.Context(), req.ID, StatusWithdrawn, Event{
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
		c.refuse(w, r, caller, http.StatusForbidden, "This is not yours to cancel",
			"Calling off an approved migration is open to the reviewer roster and to the person who raised it. Being a Toft administrator makes you an editor of this console’s configuration; it does not put you on the roster.")
		return
	}
	switch req.Status {
	case StatusApproved:
	case StatusDeploying:
		c.refuse(w, r, caller, http.StatusConflict, "This one has already started",
			"The bundle is on the target and building there. The console cannot recall it — what happens next is decided on that install, and rolling back a release that lands is done there too.")
		return
	default:
		c.refuse(w, r, caller, http.StatusConflict, "There is nothing to cancel",
			"Only an approved migration that has not started can be called off. This one is "+statusLabel(req.Status)+".")
		return
	}
	_ = r.ParseForm()
	reason := trimForm(r, "reason")
	if reason == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Cancelling needs a reason",
			"Somebody approved this and it is being called off after the fact. A year from now the ledger is all that is left of why.")
		return
	}

	moved, err := c.store.SetStatusIf(ctx, req.ID, StatusApproved, StatusCancelled, Event{
		Action: "request.cancelled", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: reason}, map[string]any{"decision_reason": reason})
	if err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "The cancellation could not be saved", err.Error())
		return
	}
	if !moved {
		// The tick got there first. Said plainly rather than retried: the push
		// has begun, and the honest answer is the same 409 as above.
		c.refuse(w, r, caller, http.StatusConflict, "The push started while you were cancelling",
			"A tick claimed this request a moment ago and the bundle is on its way to the target. Nothing was cancelled — the request page shows where it got to.")
		return
	}
	c.notifyRequester(ctx, req, "Your migration was cancelled",
		fmt.Sprintf("%s called off the approved migration of %s before it started.\n\nReason: %s",
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
		c.refuse(w, r, caller, http.StatusForbidden, "This is not yours to unschedule", "A reviewer or the requester can — and the same two can cancel it outright.")
		return
	}
	if req.Status != StatusApproved {
		c.refuse(w, r, caller, http.StatusConflict, "Nothing is scheduled", "Only an approved request carries a time.")
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
		http.Error(w, "this request has been decided and its note is now part of the record", http.StatusConflict)
		return
	}
	if !(req.RequesterID == caller.ID() || caller.Reviewer || caller.Editor || isViewer(viewers, caller.ID())) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	if Terminal(req.Status) || req.Status == StatusDeploying {
		http.Error(w, "the destination is fixed once the push has started", http.StatusConflict)
		return
	}
	if !(req.RequesterID == caller.ID() || caller.Reviewer || caller.Editor) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	slug := sanitizeSlug(trimForm(r, "destination_slug"))
	if slug == "" {
		http.Error(w, "a slug is required — it is what the app answers on over there", http.StatusUnprocessableEntity)
		return
	}
	vis := trimForm(r, "destination_visibility")
	switch vis {
	case "", "private", "authenticated", "public":
	default:
		http.Error(w, "visibility is private, authenticated or public", http.StatusUnprocessableEntity)
		return
	}
	if err := c.store.UpdateRequestFields(r.Context(), req.ID, Event{
		Action: "request.destination_changed", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: fmt.Sprintf("landing as %s, %s", slug, visibilityWord(vis))},
		map[string]any{"destination_slug": slug, "destination_visibility": vis}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.DestinationSlug, req.DestinationVisibility = slug, vis
	link, _ := c.store.FindLink(r.Context(), req.TargetID, req.SourceAppID)
	c.render.fragment(w, "request", "destination", RequestData{
		Request: *req, Link: link, Viewers: viewers, MayEdit: true,
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
	return "with whatever visibility it already has"
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
		http.Error(w, "this request has been decided", http.StatusConflict)
		return
	}
	if req.RequesterID != caller.ID() && !caller.Editor {
		http.Error(w, "only the person who raised a request may add people to it", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	id := trimForm(r, "user_id")
	name := trimForm(r, "user_name")
	if id == "" {
		http.Error(w, "a Toft user id is required", http.StatusUnprocessableEntity)
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
		http.Error(w, "forbidden", http.StatusForbidden)
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
