package main

import (
	"net/http"
	"strings"
)

// AppData is the per-source-app view: everything ever shipped from this app,
// across every target, with its links, its pushes, its drift and its checks.
//
// It exists because the question people actually ask is not *what happened to
// request 412* but *what has ever been shipped from this app, and where is it
// now*. Neither install can answer it — the source's audit log has no row at
// all for a release being downloaded, and the target's names `deploy_key:{id}`
// and nothing about which request it was — which is exactly why this console's
// ledger is a record rather than a cache.
type AppData struct {
	AppID       string
	App         *App
	Requests    []Request
	Links       []LinkState
	Events      []Event
	Note        *AppNote
	Attachments []Attachment
	TargetLabel map[string]string
	MayEdit     bool
	MayRaise    bool
}

// mayTouchApp is the same predicate the app picker uses: an app this person
// owns, or one somebody named them on in a request. Never a re-derivation of
// Toft's grant ladder — that lives in auth.CanRead and has exactly one
// implementation.
func (c *Console) mayTouchApp(r *http.Request, caller Caller, appID string) bool {
	if caller.Viewer == nil || appID == "" {
		return false
	}
	if caller.Editor || caller.Reviewer {
		return true
	}
	apps, _, err := c.raisableApps(r.Context(), caller)
	if err != nil {
		return false
	}
	for _, a := range apps {
		if a.ID == appID {
			return true
		}
	}
	return false
}

func (c *Console) handleAppActivity(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	appID := r.PathValue("appID")
	if !c.mayTouchApp(r, caller, appID) {
		c.refuse(w, r, caller, http.StatusNotFound, "No such app here",
			"The console shows the apps you own — personally or through a group — and the apps somebody has named you on in an earlier request.")
		return
	}
	ctx := r.Context()
	data := AppData{AppID: appID, TargetLabel: map[string]string{}, MayEdit: true, MayRaise: true}
	data.Requests, _ = c.store.RequestsForApp(ctx, appID)
	data.Events, _ = c.store.AppEvents(ctx, appID)
	data.Note, _ = c.store.AppNote(ctx, appID)
	data.Attachments, _ = c.store.AppAttachments(ctx, appID)

	targets, _ := c.store.ListTargets(ctx)
	for _, t := range targets {
		data.TargetLabel[t.ID] = t.Label
	}
	for _, ls := range c.linkStates(ctx, caller) {
		if ls.SourceAppID == appID {
			data.Links = append(data.Links, ls)
		}
	}
	if source, _ := c.store.SourceTarget(ctx); source != nil {
		if p, err := c.platformFor(source); err == nil {
			data.App, _ = p.App(ctx, appID)
		}
	}

	v := c.view(r, "App activity", "queue", caller, data)
	c.render.page(w, http.StatusOK, "app", v)
}

// handleAppNote writes the standing note for a source app.
//
// Where a request note says "why this release, this week", this says what a
// reviewer needs to know about this app **every** time: the change reference,
// the on-call rota, the thing that broke last quarter. It is shown on every
// request raised for the app, so it is written once rather than pasted into
// each one.
func (c *Console) handleAppNote(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	appID := r.PathValue("appID")
	if !c.mayTouchApp(r, caller, appID) {
		http.Error(w, "you can write a note on an app you own, or one somebody has named you on", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	body := strings.TrimSpace(r.FormValue("body"))
	n := AppNote{SourceAppID: appID, Body: body, UpdatedByID: caller.ID(), UpdatedByName: caller.Name()}
	if err := c.store.SaveAppNote(r.Context(), n, Event{
		Action: "app.note_edited", ActorID: caller.ID(), ActorName: caller.Name(),
		SourceAppID: appID, Detail: firstLine(body)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saved, _ := c.store.AppNote(r.Context(), appID)
	c.render.fragment(w, "app", "appnote", AppData{AppID: appID, Note: saved, MayEdit: true})
}
