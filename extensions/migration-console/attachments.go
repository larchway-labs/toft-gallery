package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Attachments: PDFs render, everything else downloads, and the content type is
// never the uploader's.
//
// Serving user-uploaded bytes from an app's own origin is the classic mistake,
// and this one has more than the usual consequence. An install may run
// **single-origin** — APPS_PORT == PORT — which is a supported configuration.
// On such an install an attachment that executed would execute on the
// platform's own origin, against the viewer's platform session. This app cannot
// know which shape the install it landed on has, so it behaves as though it is
// always the dangerous one.
//
// Three rules, and they are the whole mitigation:
//
//  1. **The stored content type is never echoed back.** The console decides
//     from an allow-list of exactly one entry.
//  2. **X-Content-Type-Options: nosniff on every attachment response**, so a
//     browser cannot decide it knows better.
//  3. **Nothing else ever renders inline** — not HTML, not SVG, not a text
//     file — and the list is the specification rather than a default with
//     exceptions.
const inlinePDF = "application/pdf"

// defaultAttachmentMaxMB bounds one file. An administrator can lower it on
// Settings; nothing raises it past the platform's own upload ceiling, because a
// document this app cannot hold is a document nobody can attach.
const defaultAttachmentMaxMB = 25

func (c *Console) attachmentLimit(r *http.Request) int64 {
	settings, _ := c.store.Settings(r.Context())
	mb := defaultAttachmentMaxMB
	if v := settings[SettingAttachmentMaxMB]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			mb = n
		}
	}
	return int64(mb) << 20
}

// readUpload takes the file off a multipart form under the cap, and returns the
// bytes plus their digest.
func readUpload(r *http.Request, limit int64) (name string, body []byte, sum string, err error) {
	if err := r.ParseMultipartForm(limit); err != nil {
		return "", nil, "", fmt.Errorf("the upload didn’t arrive: %v", err)
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		return "", nil, "", fmt.Errorf("choose a file")
	}
	defer f.Close()
	if header.Size > limit {
		return "", nil, "", fmt.Errorf("that file is %s and the limit here is %s",
			humanBytes(header.Size), humanBytes(limit))
	}
	body, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", nil, "", err
	}
	if int64(len(body)) > limit {
		return "", nil, "", fmt.Errorf("that file is over the %s limit", humanBytes(limit))
	}
	// The name is the uploader's and is treated as one. `mime/multipart` has
	// already reduced it to its last segment before it is read here — Go's
	// Part.FileName calls filepath.Base — so this app never sees a path even
	// when one was sent, and no refusal here could name what was refused. The
	// base is taken again anyway, because relying on a detail of the standard
	// library to hold a directory traversal is the kind of dependency that
	// stops being true quietly.
	name = path.Base(strings.ReplaceAll(header.Filename, `\`, "/"))
	if name == "" || name == "." || name == "/" || strings.HasPrefix(name, ".") {
		return "", nil, "", fmt.Errorf("rename the file and try again")
	}
	h := sha256.Sum256(body)
	return name, body, hex.EncodeToString(h[:]), nil
}

// isPDF decides from the bytes, not from what the uploader said they were.
func isPDF(body []byte) bool {
	return len(body) > 4 && string(body[:4]) == "%PDF"
}

// handleAddRequestAttachment: evidence for one decision.
//
// Anyone who may open the request may attach to it — the requester, the people
// they named, and the reviewers. A change board that can only read what the
// submitter thought to include is a change board that asks for the rest by
// email, which is exactly where a record stops being one.
func (c *Console) handleAddRequestAttachment(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	req, viewers, ok := c.loadRequest(w, r, caller)
	if !ok {
		return
	}
	// Closed with the request, as the note is: deleting one is already refused
	// on a finished request because what the reviewer read stays with it, and
	// a record that can grow after the fact but never shrink is not one either.
	if Terminal(req.Status) || req.Status == StatusDeploying {
		http.Error(w, "This request has finished, so documents can’t be added to it.", http.StatusConflict)
		return
	}
	limit := c.attachmentLimit(r)
	name, body, sum, err := readUpload(r, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	ct := "application/octet-stream"
	if isPDF(body) {
		ct = inlinePDF
	}
	a := Attachment{
		ID: newID(), Scope: AttachRequest, RequestID: req.ID, SourceAppID: req.SourceAppID,
		Filename: name, ContentType: ct, SizeBytes: int64(len(body)), SHA256: sum,
		UploadedByID: caller.ID(), UploadedByName: caller.Name(),
	}
	if err := c.store.AddAttachment(r.Context(), a, body, Event{
		Action: "attachment.added", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: req.ID, SourceAppID: req.SourceAppID,
		Detail: fmt.Sprintf("%s (%s)", name, humanBytes(a.SizeBytes))}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list, _ := c.store.RequestAttachments(r.Context(), req.ID)
	appList, _ := c.store.AppAttachments(r.Context(), req.SourceAppID)
	c.render.fragment(w, "request", "attachments", RequestData{
		Request: *req, Attachments: list, AppAttach: appList, Viewers: viewers, MayEdit: true})
}

// handleAddAppAttachment: the standing paperwork every request for this app
// inherits, so a change board stops re-attaching the same architecture note to
// every migration.
func (c *Console) handleAddAppAttachment(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	appID := r.PathValue("appID")
	if !c.mayTouchApp(r, caller, appID) {
		http.Error(w, "You can attach documents only to an app you own or have been added to on a request.", http.StatusForbidden)
		return
	}
	limit := c.attachmentLimit(r)
	name, body, sum, err := readUpload(r, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	ct := "application/octet-stream"
	if isPDF(body) {
		ct = inlinePDF
	}
	a := Attachment{
		ID: newID(), Scope: AttachApp, SourceAppID: appID, Filename: name,
		ContentType: ct, SizeBytes: int64(len(body)), SHA256: sum,
		UploadedByID: caller.ID(), UploadedByName: caller.Name(),
	}
	if err := c.store.AddAttachment(r.Context(), a, body, Event{
		Action: "app_attachment.added", ActorID: caller.ID(), ActorName: caller.Name(),
		SourceAppID: appID, Detail: fmt.Sprintf("%s (%s)", name, humanBytes(a.SizeBytes))}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list, _ := c.store.AppAttachments(r.Context(), appID)
	c.render.fragment(w, "app", "appattachments", AppData{AppID: appID, Attachments: list, MayEdit: true})
}

// handleServeAttachment is the one place bytes go back out, on the three rules
// above. It is gated by whatever the attachment hangs off.
func (c *Console) handleServeAttachment(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	a, body, err := c.store.GetAttachment(r.Context(), r.PathValue("aid"))
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	if !c.mayReadAttachment(r, caller, a) {
		http.NotFound(w, r)
		return
	}

	// Never the stored type: the allow-list decides, and it has one entry.
	ct := "application/octet-stream"
	disposition := "attachment"
	if a.ContentType == inlinePDF && isPDF(body) {
		ct, disposition = inlinePDF, "inline"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A filename* is not attempted: the name is the uploader's text and the
	// quoting rules around it are exactly the kind of thing that goes wrong
	// quietly. The id is stable and the page beside it carries the real name.
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, safeFilename(a.Filename)))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	// Nothing here may be framed by anything but this app, and nothing on this
	// response may run.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'; sandbox")
	_, _ = w.Write(body)
}

func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127 || r == '"' || r == '\\':
			b.WriteByte('_')
		case r > 126:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "attachment"
	}
	return out
}

func (c *Console) mayReadAttachment(r *http.Request, caller Caller, a *Attachment) bool {
	if a.Scope == AttachRequest && a.RequestID != "" {
		req, err := c.store.GetRequest(r.Context(), a.RequestID)
		if err != nil || req == nil {
			return false
		}
		_, ok := c.requestAudience(r.Context(), caller, req)
		return ok
	}
	return c.mayTouchApp(r, caller, a.SourceAppID)
}

func (c *Console) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	caller := c.resolveCaller(r)
	if c.refuseUnidentified(w, r, caller) {
		return
	}
	if caller.Viewer == nil {
		http.Error(w, "You don’t have permission to do that.", http.StatusForbidden)
		return
	}
	a, _, err := c.store.GetAttachment(r.Context(), r.PathValue("aid"))
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	if !c.mayReadAttachment(r, caller, a) {
		http.NotFound(w, r)
		return
	}
	// Only the person who attached it, or an editor. An approver removing the
	// evidence they were shown is the one deletion this app must not make easy.
	if a.UploadedByID != caller.ID() && !caller.Editor {
		http.Error(w, "Only the person who attached this, or a console editor, can remove it.", http.StatusForbidden)
		return
	}
	if a.Scope == AttachRequest {
		req, _ := c.store.GetRequest(r.Context(), a.RequestID)
		if req != nil && Terminal(req.Status) {
			http.Error(w, "This request has been decided, so its documents can’t be removed.", http.StatusConflict)
			return
		}
	}
	if err := c.store.DeleteAttachment(r.Context(), a.ID, Event{
		Action: "attachment.removed", ActorID: caller.ID(), ActorName: caller.Name(),
		RequestID: a.RequestID, SourceAppID: a.SourceAppID, Detail: a.Filename}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Scope == AttachRequest {
		req, _ := c.store.GetRequest(r.Context(), a.RequestID)
		list, _ := c.store.RequestAttachments(r.Context(), a.RequestID)
		appList, _ := c.store.AppAttachments(r.Context(), a.SourceAppID)
		viewers, _ := c.store.ListRequestViewers(r.Context(), a.RequestID)
		c.render.fragment(w, "request", "attachments", RequestData{
			Request: *req, Attachments: list, AppAttach: appList, Viewers: viewers, MayEdit: true})
		return
	}
	list, _ := c.store.AppAttachments(r.Context(), a.SourceAppID)
	c.render.fragment(w, "app", "appattachments", AppData{AppID: a.SourceAppID, Attachments: list, MayEdit: true})
}
