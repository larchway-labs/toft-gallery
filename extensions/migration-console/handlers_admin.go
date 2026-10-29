package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// platformFor builds a client for a target row, decrypting its key.
//
// Nothing caches a decrypted key: it lives for the length of one call, which is
// not a boundary — this process holds the secret either way — but is the
// difference between a heap dump containing one key and a heap dump containing
// the estate.
func (c *Console) platformFor(t *Target) (*Platform, error) {
	if t == nil {
		return nil, fmt.Errorf("target not found")
	}
	key, err := c.seal.open(t.DeployKeyEnc)
	if err != nil {
		return nil, err
	}
	return newPlatform(t.BaseURL, key), nil
}

func (c *Console) requireEditor(w http.ResponseWriter, r *http.Request, caller Caller) bool {
	if caller.Editor {
		return true
	}
	c.refuse(w, r, caller, http.StatusForbidden, "Only this app’s owners and editors can change this",
		"Changing targets, reviewers, links and settings needs the owner or editor role on this app in Toft.")
	return false
}

// --- Targets ----------------------------------------------------------------

type TargetsData struct {
	Targets []Target
	Source  *Target
	Groups  []Group
	Ranges  string
	// EditingGroups is the group directory of the row being edited, read with
	// that row's own key — what makes `default_owner_group_id` a picker rather
	// than a UUID copied off another install. Absent on the create form,
	// because there is no row to ask yet.
	EditingGroups    []Group
	EditingGroupsWhy string
	// Editing is the row the form is standing in for, when somebody chose one.
	// Without it the form could only ever add: the handler has always known how
	// to replace a row and keep its stored key, and nothing in the interface
	// ever supplied the id it needed, so changing a label meant a second row
	// with the credential pasted again and the old row left behind.
	Editing *Target
	// OwnAddress is this install's address as the platform gave it to the app
	// (TOFT_API_URL) — what the source row should say, offered so nobody has to
	// work out how an app reaches the install it runs on.
	OwnAddress string
	// Chains is every promotion chain read from the top, "Stage → Prod".
	Chains []string
}

func (c *Console) handleTargets(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	ctx := r.Context()
	data := TargetsData{OwnAddress: ownOrigin(c.cfg.APIURL)}
	all, _ := c.store.ListTargets(ctx)
	labelTargets(all)
	required := map[string]bool{}
	for _, t := range all {
		required[t.RequiresTargetID] = true
	}
	for _, t := range all {
		if t.Kind == TargetKindSource {
			s := t
			data.Source = &s
			continue
		}
		data.Targets = append(data.Targets, t)
		// A chain is printed once, from its last link.
		if t.Gated() && !required[t.ID] {
			data.Chains = append(data.Chains, chainOf(all, t))
		}
	}
	if id := r.URL.Query().Get("edit"); id != "" {
		data.Editing, _ = c.store.GetTarget(ctx, id)
		if data.Editing != nil {
			for _, t := range data.Targets {
				if t.ID == data.Editing.ID {
					data.Editing.RequiresLabel = t.RequiresLabel
				}
			}
		}
		// The default owning group is a group on **that** install, not this
		// one, so it can only be offered once the row exists and its key has
		// been tested — which is exactly when somebody is editing it. On the
		// create form it stays a text field, because there is nothing to ask
		// yet. Same read as the approval screen's, same reason.
		if data.Editing != nil && data.Editing.Kind == TargetKindTarget {
			data.EditingGroups, data.EditingGroupsWhy = c.targetGroups(ctx, data.Editing)
		}
	}
	settings, _ := c.store.Settings(ctx)
	data.Ranges = settings[SettingPrivateRangesOK]

	v := c.view(r, "Targets", "targets", caller, data)
	v.Setup = c.setupSteps(ctx, caller, v.Caps)
	c.render.page(w, http.StatusOK, "targets", v)
}

// labelTargets fills in each row's RequiresLabel from the others.
func labelTargets(all []Target) {
	labels := map[string]string{}
	for _, t := range all {
		labels[t.ID] = t.Label
	}
	for i := range all {
		all[i].RequiresLabel = labels[all[i].RequiresTargetID]
	}
}

// handleSaveTarget writes a target row, and tests the connection **before**
// saving it. Nothing is saved on a failed test.
func (c *Console) handleSaveTarget(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()

	kind := trimForm(r, "kind")
	if kind != TargetKindSource {
		kind = TargetKindTarget
	}
	label := trimForm(r, "label")
	if label == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A target needs a label",
			"Name it after the environment, such as Production.")
		return
	}
	settings, _ := c.store.Settings(ctx)
	ranges := strings.Split(settings[SettingPrivateRangesOK], ",")
	var base string
	var err error
	if kind == TargetKindSource {
		base, err = sourceAddress(trimForm(r, "base_url"), c.cfg.APIURL, ranges)
	} else {
		base, err = validateTargetURL(trimForm(r, "base_url"), ranges)
	}
	if err != nil {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That address can’t be used", err.Error())
		return
	}

	id := trimForm(r, "id")
	existing, _ := c.store.GetTarget(ctx, id)

	key := trimForm(r, "deploy_key")
	// The key is write-only in the interface: entered once, never rendered
	// back, displayed thereafter as its prefix alone. Changing it is replacing
	// it, and leaving it blank on an existing row keeps what is stored.
	var enc, prefix string
	switch {
	case key != "":
		enc, err = c.seal.seal(key)
		if err != nil {
			c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t encrypt the key", err.Error())
			return
		}
		if len(key) > 10 {
			prefix = key[:10]
		} else {
			prefix = key
		}
	case existing != nil:
		enc, prefix = existing.DeployKeyEnc, existing.KeyPrefix
	default:
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A target needs a key",
			"Ask an administrator on that install to mint a deploy key, then paste it here.")
		return
	}

	t := &Target{
		ID: id, Kind: kind, Label: label, BaseURL: base,
		DeployKeyEnc: enc, KeyPrefix: prefix,
		DefaultOwnerGroupID:   trimForm(r, "default_owner_group_id"),
		DefaultOwnerGroupName: trimForm(r, "default_owner_group_name"),
		FastTrack:             r.FormValue("fast_track") == "on",
		CreatedByID:           caller.ID(), CreatedByName: caller.Name(),
		CreatedAt:        time.Now().UTC(),
		RequiresTargetID: trimForm(r, "requires_target_id"),
		RequiresLockfile: r.FormValue("requires_lockfile") == "on",
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if kind == TargetKindSource {
		t.RequiresTargetID, t.RequiresLockfile = "", false
	}
	// The chain is checked before the connection is: a loop is refused however
	// reachable the install is.
	all, _ := c.store.ListTargets(ctx)
	if why := chainRefusal(all, t.ID, t.Kind, t.Label, t.RequiresTargetID); why != "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That requirement can’t be saved", why)
		return
	}
	if existing != nil {
		t.CreatedByID, t.CreatedByName, t.CreatedAt = existing.CreatedByID, existing.CreatedByName, existing.CreatedAt
	}

	// Test before saving. Nothing is written on a failure, and the message says
	// which of the two calls failed — *wrong address* and *wrong key* must stay
	// tellable apart.
	ok, result := c.testTarget(ctx, t)
	if !ok {
		c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t connect to that install", result)
		return
	}
	now := time.Now().UTC()
	t.LastTestedAt, t.LastTestOK, t.LastTestResult = &now, &ok, result

	// The default owning group's *name* comes from the target's own directory
	// and never from the form beside it. Same rule as the approval screen's,
	// and the same reason: a label somebody can type freely is a label that can
	// disagree with the id next to it, and this pair is copied onto every
	// request raised against this target afterwards. A group that has since
	// been deleted there clears the pair rather than keeping a name for an id
	// that no longer resolves.
	if t.Kind == TargetKindTarget && t.DefaultOwnerGroupID != "" {
		if p, err := c.platformFor(t); err == nil {
			if g, err := p.Group(ctx, t.DefaultOwnerGroupID); err == nil {
				t.DefaultOwnerGroupName = g.Name
			} else {
				c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group isn’t on the target",
					fmt.Sprintf("%s has no group with that id, so nothing was saved: %v", t.Label, err))
				return
			}
		}
	}

	if err := c.store.SaveTarget(ctx, t); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t save the target", err.Error())
		return
	}
	action := "target.created"
	if existing != nil {
		action = "target.updated"
		if key != "" {
			action = "target.key_replaced"
		}
	}
	_ = c.store.WriteEvent(ctx, Event{Action: action, ActorID: caller.ID(), ActorName: caller.Name(),
		TargetID: t.ID, Detail: t.Label + " · " + t.BaseURL})
	c.redirect(w, r, "/targets")
}

// testTarget makes two calls, in order, and reports which one failed.
func (c *Console) testTarget(ctx context.Context, t *Target) (bool, string) {
	p, err := c.platformFor(t)
	if err != nil {
		return false, err.Error()
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// 1. Is there a Toft at this address? /api/v1/config is the platform's
	//    own unauthenticated probe and is one of the things a chart may never
	//    template, so it is the one endpoint guaranteed to be there.
	appsURL, err := p.Reachable(callCtx)
	if err != nil {
		return false, fmt.Sprintf("No Toft install answered at that address: %v. The key wasn’t tried.", err)
	}
	if appsURL != "" {
		t.AppsURL = appsURL
	}
	// 2. Does the credential work — and is it the right scope for what this row
	//    is for? A read-scoped key on a target would 403 the push later, which
	//    is a failure nobody wants to find at 6pm on a Friday.
	if _, err := p.Apps(callCtx); err != nil {
		return false, fmt.Sprintf("The install answered, but the key didn’t work: %v", err)
	}
	if t.Kind == TargetKindTarget {
		return true, "Reached the install and the key listed its apps."
	}
	return true, "Reached this install and the read-scoped key listed its apps."
}

func (c *Console) handleTestTarget(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !caller.Editor {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	t, err := c.store.GetTarget(r.Context(), r.PathValue("id"))
	if err != nil || t == nil {
		http.NotFound(w, r)
		return
	}
	ok, result := c.testTarget(r.Context(), t)
	_ = c.store.RecordTargetTest(r.Context(), t.ID, ok, result, t.AppsURL)
	_ = c.store.WriteEvent(r.Context(), Event{Action: "target.tested",
		ActorID: caller.ID(), ActorName: caller.Name(), TargetID: t.ID, Detail: result})
	refreshed, _ := c.store.GetTarget(r.Context(), t.ID)
	if refreshed != nil && refreshed.Gated() {
		if up, _ := c.store.GetTarget(r.Context(), refreshed.RequiresTargetID); up != nil {
			refreshed.RequiresLabel = up.Label
		}
	}
	c.render.fragment(w, "targets", "targetrow", refreshed)
}

func (c *Console) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	t, _ := c.store.GetTarget(r.Context(), r.PathValue("id"))
	if t == nil {
		http.NotFound(w, r)
		return
	}
	// Refused by the foreign key anyway; checked first so the refusal names
	// the target holding it rather than passing a driver's sentence through.
	// Not SET NULL: removing Stage must not open Prod.
	if deps, _ := c.store.TargetsRequiring(r.Context(), t.ID); len(deps) > 0 {
		c.refuse(w, r, caller, http.StatusConflict, "Couldn’t remove that target",
			fmt.Sprintf("%s requires this target. Clear that on %s first.", deps[0].Label, deps[0].Label))
		return
	}
	if err := c.store.DeleteTarget(r.Context(), t.ID); err != nil {
		c.refuse(w, r, caller, http.StatusConflict, "Couldn’t remove that target",
			"Existing requests refer to it. "+err.Error())
		return
	}
	_ = c.store.WriteEvent(r.Context(), Event{Action: "target.removed",
		ActorID: caller.ID(), ActorName: caller.Name(), TargetID: t.ID, Detail: t.Label})
	c.redirect(w, r, "/targets")
}

// --- Reviewers --------------------------------------------------------------

func (c *Console) handleReviewers(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	roster, _ := c.store.ListReviewers(r.Context())
	var groups []Group
	if source, _ := c.store.SourceTarget(r.Context()); source != nil {
		if p, err := c.platformFor(source); err == nil {
			groups, _ = p.Groups(r.Context())
		}
	}
	// The people half of the same problem the group list already solved here.
	// A deploy key may read the group directory and never the user one, so
	// there is nobody to enumerate from the platform — but everyone who has
	// taken part in this console is already in its own ledger, and that is who
	// somebody adding a reviewer is usually reaching for. Offered, never
	// enforced: the id field still takes anything.
	people, _ := c.store.KnownPeople(r.Context())
	v := c.view(r, "Reviewers", "reviewers", caller, map[string]any{
		"Roster": roster, "Groups": groups, "People": people})
	c.render.page(w, http.StatusOK, "reviewers", v)
}

func (c *Console) handleAddReviewer(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	_ = r.ParseForm()
	kind := trimForm(r, "subject_type")
	if kind != "group" {
		kind = "user"
	}
	id := trimForm(r, "subject_id")
	name := trimForm(r, "subject_name")
	if id == "" {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "A reviewer needs an id",
			"Enter a person’s Toft user id, or a group id from the list to add a whole team.")
		return
	}
	if name == "" {
		name = id
	}
	if err := c.store.AddReviewer(r.Context(), Reviewer{
		ID: newID(), SubjectType: kind, SubjectID: id, SubjectName: name, AddedByName: caller.Name()}); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "Couldn’t add the reviewer", err.Error())
		return
	}
	_ = c.store.WriteEvent(r.Context(), Event{Action: "reviewer.added",
		ActorID: caller.ID(), ActorName: caller.Name(), Detail: kind + " " + name})
	c.redirect(w, r, "/reviewers")
}

func (c *Console) handleRemoveReviewer(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	_ = c.store.RemoveReviewer(r.Context(), r.PathValue("id"))
	_ = c.store.WriteEvent(r.Context(), Event{Action: "reviewer.removed",
		ActorID: caller.ID(), ActorName: caller.Name(), Detail: r.PathValue("id")})
	c.redirect(w, r, "/reviewers")
}

// --- Settings ---------------------------------------------------------------

type SettingsData struct {
	Settings map[string]string
	Groups   []Group
	EmailOn  bool
	Refusal  string
}

func (c *Console) handleSettings(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	settings, _ := c.store.Settings(r.Context())
	data := SettingsData{Settings: settings}
	if source, _ := c.store.SourceTarget(r.Context()); source != nil {
		if p, err := c.platformFor(source); err == nil {
			data.Groups, _ = p.Groups(r.Context())
		}
	}
	v := c.view(r, "Settings", "settings", caller, data)
	// Absent, not off. When `email` is not among this app's capabilities the
	// notification settings are not in the interface at all — not greyed with a
	// tooltip, not a switch that fails on save.
	data.EmailOn = v.Caps["email"]
	v.Data = data
	c.render.page(w, http.StatusOK, "settings", v)
}

func (c *Console) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()

	if groupID := trimForm(r, "admin_group_id"); groupID != "" {
		source, _ := c.store.SourceTarget(ctx)
		p, err := c.platformFor(source)
		if err != nil {
			c.refuse(w, r, caller, http.StatusBadGateway, "Couldn’t read this install", err.Error())
			return
		}
		g, err := p.Group(ctx, groupID)
		if err != nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Couldn’t read that group", err.Error())
			return
		}
		// **A group with no address is refused, and the refusal names the
		// remedy.** Not fussiness: the roster is withheld from a principal that
		// is neither an administrator nor a member, and a key that belongs to
		// nobody is neither — so the fallback the platform performs for itself,
		// expanding the members when a group has no address, is one this
		// credential cannot perform. Accepting the group anyway would mean
		// mailing nobody, silently, on exactly the events this setting is for.
		if g.Email == "" {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group has no email address",
				fmt.Sprintf("The console emails a group’s own address, and %s has none in Toft. Set an address on the group, or choose another group.", g.Name))
			return
		}
		_ = c.store.SetSetting(ctx, SettingAdminGroupID, g.ID)
		_ = c.store.SetSetting(ctx, SettingAdminGroupName, g.Name)
		_ = c.store.SetSetting(ctx, SettingAdminGroupEmail, g.Email)
	} else {
		_ = c.store.SetSetting(ctx, SettingAdminGroupID, "")
		_ = c.store.SetSetting(ctx, SettingAdminGroupName, "")
		_ = c.store.SetSetting(ctx, SettingAdminGroupEmail, "")
	}

	if v := trimForm(r, "attachment_max_mb"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 100 {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "Choose an attachment size from 1 to 100 MB",
				"Enter a whole number between 1 and 100.")
			return
		}
		_ = c.store.SetSetting(ctx, SettingAttachmentMaxMB, v)
	}
	tz := trimForm(r, "time_zone")
	if tz != "" {
		if _, err := loadZone(tz); err != nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That isn’t a time zone",
				"Use a time zone name such as America/New_York or Europe/London, or leave it blank for UTC.")
			return
		}
	}
	_ = c.store.SetSetting(ctx, SettingTimeZone, tz)
	setDisplayZone(tz)
	_ = c.store.SetSetting(ctx, SettingPrivateRangesOK, trimForm(r, "private_ranges_allowed"))
	_ = c.store.SetSetting(ctx, SettingNotifyByDefault, boolValue(r.FormValue("notify_by_default")))
	_ = c.store.SetSetting(ctx, SettingRequireNoteOnNew, boolValue(r.FormValue("require_note")))

	_ = c.store.WriteEvent(ctx, Event{Action: "settings.changed",
		ActorID: caller.ID(), ActorName: caller.Name()})
	c.redirect(w, r, "/settings")
}

func boolValue(v string) string {
	if v == "on" || v == "true" {
		return "true"
	}
	return "false"
}

// --- Health -----------------------------------------------------------------

// /health exists because the Gallery guarantees the catalog grants nothing, so
// a freshly-deployed extension is *deliberately* not working yet. It is
// readable by anyone past the gateway: a viewer who lands on an app that does
// not work needs to know who to ask, not a 403.
func (c *Console) handleHealth(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	v := c.view(r, "Install", "health", caller, nil)
	v.Setup = c.setupSteps(r.Context(), caller, v.Caps)
	c.render.page(w, http.StatusOK, "health", v)
}

// handleRunTick starts the tick now. A console editor's button: the person who
// configures targets is the person who wants to see a new one tested or a
// stuck approval moved, without waiting five minutes or opening the platform.
func (c *Console) handleRunTick(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !caller.Editor {
		c.refuse(w, r, caller, http.StatusForbidden, "Only this app’s owners and editors can process migrations now",
			"Migrations are processed every five minutes on their own.")
		return
	}
	v := c.view(r, "Install", "health", caller, nil)
	if err := c.startTick(r.Context(), r); err != nil {
		v.Notice = &Notice{Tone: "danger", Title: "Couldn’t start processing migrations", Body: err.Error()}
	} else {
		v.Notice = &Notice{Tone: "ok", Title: "Processing migrations",
			Body: "The process-migrations job is running. Its log and result are on this app’s Automation tab in Toft."}
	}
	v.Setup = c.setupSteps(r.Context(), caller, v.Caps)
	c.render.page(w, http.StatusOK, "health", v)
}

// --- The command palette ----------------------------------------------------

func (c *Console) handlePalette(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if caller.Viewer == nil {
		c.render.fragment(w, "queue", "palette-results", nil)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var hits []PaletteHit
	if q != "" {
		reqs, _ := c.store.ListRequests(r.Context())
		for _, rq := range reqs {
			if _, ok := c.requestAudience(r.Context(), caller, &rq); !ok {
				continue
			}
			hay := strings.ToLower(rq.SourceAppName + " " + rq.SourceAppSlug + " " + rq.RequesterName + " " + rq.ID)
			if strings.Contains(hay, q) || strconv.FormatInt(rq.Seq, 10) == q {
				hits = append(hits, PaletteHit{
					Href:  c.urls.Path("/requests/" + rq.ID),
					Title: fmt.Sprintf("#%d · %s", rq.Seq, rq.SourceAppName),
					Sub:   statusLabel(rq.Status) + " · raised by " + rq.RequesterName})
			}
			if len(hits) >= 12 {
				break
			}
		}
	}
	c.render.fragment(w, "queue", "palette-results", hits)
}
