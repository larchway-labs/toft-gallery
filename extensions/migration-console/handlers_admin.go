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
		return nil, fmt.Errorf("no such target")
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
	c.refuse(w, r, caller, http.StatusForbidden, "This is the console’s configuration",
		"Writing targets, the roster, the links and the settings needs owner or editor on this app in Toft. Raising a request does not.")
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
}

func (c *Console) handleTargets(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !c.requireEditor(w, r, caller) {
		return
	}
	ctx := r.Context()
	data := TargetsData{}
	all, _ := c.store.ListTargets(ctx)
	for _, t := range all {
		if t.Kind == TargetKindSource {
			s := t
			data.Source = &s
			continue
		}
		data.Targets = append(data.Targets, t)
	}
	if id := r.URL.Query().Get("edit"); id != "" {
		data.Editing, _ = c.store.GetTarget(ctx, id)
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
			"Name it after the environment, not after the machine — in six months the label is the only clue to what it is.")
		return
	}
	settings, _ := c.store.Settings(ctx)
	base, err := validateTargetURL(trimForm(r, "base_url"), strings.Split(settings[SettingPrivateRangesOK], ","))
	if err != nil {
		c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That address cannot be used", err.Error())
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
			c.refuse(w, r, caller, http.StatusInternalServerError, "The key could not be encrypted", err.Error())
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
			"An administrator on that install mints it and pastes it here. No credential travels between installs on its own, and no Toft can mint one for another.")
		return
	}

	t := &Target{
		ID: id, Kind: kind, Label: label, BaseURL: base,
		DeployKeyEnc: enc, KeyPrefix: prefix,
		DefaultOwnerGroupID:   trimForm(r, "default_owner_group_id"),
		DefaultOwnerGroupName: trimForm(r, "default_owner_group_name"),
		FastTrack:             r.FormValue("fast_track") == "on",
		CreatedByID:           caller.ID(), CreatedByName: caller.Name(),
		CreatedAt: time.Now().UTC(),
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if existing != nil {
		t.CreatedByID, t.CreatedByName, t.CreatedAt = existing.CreatedByID, existing.CreatedByName, existing.CreatedAt
	}

	// Test before saving. Nothing is written on a failure, and the message says
	// which of the two calls failed — *wrong address* and *wrong key* must stay
	// tellable apart.
	ok, result := c.testTarget(ctx, t)
	if !ok {
		c.refuse(w, r, caller, http.StatusBadGateway, "That connection did not work", result)
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
				c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group is not on the target",
					fmt.Sprintf("%s does not have a group with that id, so nothing was saved: %v", t.Label, err))
				return
			}
		}
	}

	if err := c.store.SaveTarget(ctx, t); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "The target could not be saved", err.Error())
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
	if err := p.Reachable(callCtx); err != nil {
		return false, fmt.Sprintf("The address did not answer as a Toft: %v. The key was not tried.", err)
	}
	// 2. Does the credential work — and is it the right scope for what this row
	//    is for? A read-scoped key on a target would 403 the push later, which
	//    is a failure nobody wants to find at 6pm on a Friday.
	if _, err := p.Apps(callCtx); err != nil {
		return false, fmt.Sprintf("The install answered, but the key did not work: %v", err)
	}
	if t.Kind == TargetKindTarget {
		return true, "Reached the install and the key listed its apps."
	}
	return true, "Reached this install and the read-scoped key listed its apps."
}

func (c *Console) handleTestTarget(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if !caller.Editor {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	t, err := c.store.GetTarget(r.Context(), r.PathValue("id"))
	if err != nil || t == nil {
		http.NotFound(w, r)
		return
	}
	ok, result := c.testTarget(r.Context(), t)
	_ = c.store.RecordTargetTest(r.Context(), t.ID, ok, result)
	_ = c.store.WriteEvent(r.Context(), Event{Action: "target.tested",
		ActorID: caller.ID(), ActorName: caller.Name(), TargetID: t.ID, Detail: result})
	refreshed, _ := c.store.GetTarget(r.Context(), t.ID)
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
	if err := c.store.DeleteTarget(r.Context(), t.ID); err != nil {
		c.refuse(w, r, caller, http.StatusConflict, "That target could not be removed",
			"Requests already point at it, and the ledger keeps them. "+err.Error())
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
			"For a person, their Toft user id. For a team, pick the group from the list — one row, and everyone in it reviews.")
		return
	}
	if name == "" {
		name = id
	}
	if err := c.store.AddReviewer(r.Context(), Reviewer{
		ID: newID(), SubjectType: kind, SubjectID: id, SubjectName: name, AddedByName: caller.Name()}); err != nil {
		c.refuse(w, r, caller, http.StatusInternalServerError, "The reviewer could not be added", err.Error())
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
			c.refuse(w, r, caller, http.StatusBadGateway, "This install could not be read", err.Error())
			return
		}
		g, err := p.Group(ctx, groupID)
		if err != nil {
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group could not be read", err.Error())
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
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That group has no address",
				fmt.Sprintf("%s has no email address on Toft, and this console can only mail a group's own address — it cannot read who is in a group. Set an address on the group, or pick another group.", g.Name))
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
			c.refuse(w, r, caller, http.StatusUnprocessableEntity, "That attachment size will not work",
				"Between 1 and 100 MB. The platform's own upload ceiling is 100MB, and a document this console cannot hold is one nobody can attach.")
			return
		}
		_ = c.store.SetSetting(ctx, SettingAttachmentMaxMB, v)
	}
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
