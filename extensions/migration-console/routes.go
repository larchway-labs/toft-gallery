package main

import (
	"net/http"
	"strings"
)

// Every surface is inside this app. The platform gains no route, no page and no
// nav item; on Toft this is one card in the Gallery and one app in the app
// list.
func (c *Console) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.FileServerFS(staticFS))

	// The queue, and what people arrive with.
	mux.HandleFunc("GET /{$}", c.handleQueue)
	mux.HandleFunc("GET /palette", c.handlePalette)
	mux.HandleFunc("GET /health", c.handleHealth)

	// Raising one.
	mux.HandleFunc("GET /requests/new", c.handleNewRequestForm)
	mux.HandleFunc("POST /requests/new", c.handleCreateRequest)
	mux.HandleFunc("GET /releases", c.handleReleasePicker)

	// One request.
	mux.HandleFunc("GET /requests/{id}", c.handleRequest)
	mux.HandleFunc("GET /requests/{id}/progress", c.handleRequestProgress)
	mux.HandleFunc("POST /requests/{id}/withdraw", c.handleWithdraw)
	mux.HandleFunc("POST /requests/{id}/approve", c.handleApprove)
	mux.HandleFunc("POST /requests/{id}/reject", c.handleReject)
	mux.HandleFunc("POST /requests/{id}/cancel", c.handleCancel)
	mux.HandleFunc("POST /requests/{id}/unschedule", c.handleUnschedule)
	mux.HandleFunc("POST /requests/{id}/note", c.handleRequestNote)
	mux.HandleFunc("POST /requests/{id}/destination", c.handleDestination)
	mux.HandleFunc("POST /requests/{id}/viewers", c.handleAddViewer)
	mux.HandleFunc("POST /requests/{id}/viewers/{userID}/remove", c.handleRemoveViewer)
	mux.HandleFunc("POST /requests/{id}/attachments", c.handleAddRequestAttachment)

	// One source app: everything ever shipped from it, and its standing note.
	mux.HandleFunc("GET /apps/{appID}", c.handleAppActivity)
	mux.HandleFunc("POST /apps/{appID}/note", c.handleAppNote)
	mux.HandleFunc("POST /apps/{appID}/attachments", c.handleAddAppAttachment)

	// Attachments are addressed by their own id and gated by what they hang
	// off, so there is one implementation of the serving rules rather than two.
	mux.HandleFunc("GET /attachments/{aid}", c.handleServeAttachment)
	mux.HandleFunc("POST /attachments/{aid}/delete", c.handleDeleteAttachment)

	// Configuration — the console editor's, which is the platform's owner or
	// editor on this app.
	mux.HandleFunc("GET /targets", c.handleTargets)
	mux.HandleFunc("POST /targets", c.handleSaveTarget)
	mux.HandleFunc("POST /targets/{id}/test", c.handleTestTarget)
	mux.HandleFunc("POST /targets/{id}/delete", c.handleDeleteTarget)
	mux.HandleFunc("GET /reviewers", c.handleReviewers)
	mux.HandleFunc("POST /reviewers", c.handleAddReviewer)
	mux.HandleFunc("POST /reviewers/{id}/delete", c.handleRemoveReviewer)
	mux.HandleFunc("GET /links", c.handleLinks)
	mux.HandleFunc("POST /links/{id}/delete", c.handleDeleteLink)
	mux.HandleFunc("POST /links/{id}/send", c.handleSendNewest)
	mux.HandleFunc("GET /settings", c.handleSettings)
	mux.HandleFunc("POST /settings", c.handleSaveSettings)

	// Run the tick now — a console editor's button on /health. The tick itself
	// is a job, not a path: there is no route here the platform calls.
	mux.HandleFunc("POST /health/tick", c.handleRunTick)

	return c.securityHeaders(mux)
}

// securityHeaders is more than hygiene here, and the reason is stated where the
// attachment rules are: an install may run single-origin (APPS_PORT == PORT),
// which is supported, and on such an install an attachment that executed would
// execute on the platform's own origin against the viewer's platform session.
//
// The platform sets no CSP on a proxied response, so this app sets its own. It
// cannot know which shape the install it landed on has, so it behaves as though
// it is always the dangerous one.
func (c *Console) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/attachments/") {
			w.Header().Set("Content-Security-Policy",
				"default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self'; "+
					"font-src 'self'; img-src 'self' data:; object-src 'self'; form-action 'self'; "+
					"frame-ancestors 'self'; base-uri 'none'; connect-src 'self'")
		}
		next.ServeHTTP(w, r)
	})
}
