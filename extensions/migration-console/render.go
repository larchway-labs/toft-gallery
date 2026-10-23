package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// renderer holds one template set per page: the layout, every partial, and that
// page's own file. Built once at start — a template parsed per request is a
// cost paid on a page somebody is waiting for.
type renderer struct {
	pages map[string]*template.Template
	urls  urls
}

func newRenderer(u urls) *renderer {
	r := &renderer{pages: map[string]*template.Template{}, urls: u}

	shared, err := fs.Glob(templateFS, "templates/_*.html")
	if err != nil {
		log.Fatal(err)
	}
	shared = append(shared, "templates/layout.html")

	pages, err := fs.Glob(templateFS, "templates/[a-z]*.html")
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range pages {
		if p == "templates/layout.html" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		files := append(append([]string{}, shared...), p)
		t, err := template.New("layout.html").Funcs(r.funcs()).ParseFS(templateFS, files...)
		if err != nil {
			log.Fatalf("parsing %s: %v", p, err)
		}
		r.pages[name] = t
	}
	return r
}

func (r *renderer) funcs() template.FuncMap {
	return template.FuncMap{
		// Every app-relative URL goes through this, hrefs and htmx attributes
		// alike. See basepath.go for the asymmetry it exists to hold.
		"url":   r.urls.Path,
		"tone":  tone,
		"label": statusLabel,
		// Absolute times carry their zone, and that is not decoration. Every
		// one of these is rendered on the install's clock rather than the
		// reader's, and the field that *takes* a time — a scheduled
		// migration — is read on the same clock while the browser's widget
		// shows the reader's. A stamp with no zone on it is the thing that
		// makes those two impossible to reconcile.
		"when": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return inZone(t, "2 Jan 2006, 15:04 MST")
		},
		"whenp": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "—"
			}
			return inZone(*t, "2 Jan 2006, 15:04 MST")
		},
		// A release label is one string — "main@aa0a1e3e · 27 Aug 2026 19:01 · by
		// manual · python 3.14.7" — because on a request page every part of it
		// is worth reading. In a list it is the widest column by a factor of
		// three and it pushes the app's own name into two lines, so a table
		// takes the reference and the time and leaves the rest.
		"relref":  func(s string) string { return relField(s, 0) },
		"relwhen": func(s string) string { return relField(s, 1) },
		// The install's clock, now — so somebody typing a window into a
		// datetime-local field can see what it will be read as.
		"installclock": func() string { return inZone(time.Now(), "2 Jan 2006, 15:04 MST") },
		// The zone's name as the settings page and the schedule hint print it.
		"zonename": func() string {
			if zoneSet() {
				return zone().String()
			}
			return ""
		},
		"zones": func() []string { return commonZones },
		"ago":   ago,
		"bytes": humanBytes,
		"short": func(n int, s string) string {
			if len(s) <= n {
				return s
			}
			return s[:n]
		},
		"has": func(m map[string]bool, k string) bool { return m[k] },
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				k, _ := kv[i].(string)
				m[k] = kv[i+1]
			}
			return m
		},
	}
}

func (r *renderer) page(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.pages[name]
	if !ok {
		http.Error(w, "Couldn’t show this page.", http.StatusInternalServerError)
		return
	}
	// Rendered into a buffer first, and this is the whole reason: a template
	// that fails halfway has already written half a document to the socket, and
	// what the browser gets is not an error but a page that stops mid-tag. A
	// blank where a control should be is the hardest failure in this app to
	// see, so the buffer is what makes it loud.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		log.Printf("rendering %s: %v", name, err)
		http.Error(w, "Couldn’t show this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// fragment renders one named block for an htmx swap — the region that changed,
// and nothing else on the page moves.
func (r *renderer) fragment(w http.ResponseWriter, page, block string, data any) {
	t, ok := r.pages[page]
	if !ok {
		http.Error(w, "Couldn’t show this page.", http.StatusInternalServerError)
		return
	}
	// Same rule as page, and it matters more: an htmx swap of a truncated
	// fragment replaces a working region with a broken one and says nothing. A
	// 500 is a swap htmx declines to make, which leaves what was there.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, data); err != nil {
		log.Printf("rendering %s/%s: %v", page, block, err)
		http.Error(w, "Couldn’t show this part of the page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// tone maps a word to one of the five, in one place so two surfaces cannot
// disagree.
//
// Drift is `attention` and never `danger`: it is not a failure, it is a fact
// the approver needs. Reaching for red to mean *notice this* is how a warning
// colour stops working.
func tone(status string) string {
	switch status {
	case StatusDeployed, LinkUpToDate, "ok", "clean", "pass", "active":
		return "ok"
	case StatusSubmitted, StatusApproved, StatusDeploying, LinkBehind, LinkDiverged, "drift", "warn":
		return "attention"
	case StatusFailed, StatusRejected, "fail", "vulnerable", "blocked":
		return "danger"
	case StatusWithdrawn, StatusCancelled, StatusDraft, LinkNotChecked, "skipped", "absent", "":
		return "neutral"
	case RoleSystem, "tick":
		return "info"
	}
	return "neutral"
}

// statusLabel is the word beside the colour. Every status carries one, because
// a colour is never the only carrier.
func statusLabel(status string) string {
	switch status {
	case StatusDraft:
		return "Draft"
	case StatusSubmitted:
		return "Awaiting review"
	case StatusApproved:
		return "Approved"
	case StatusDeploying:
		return "Deploying"
	case StatusDeployed:
		return "Deployed"
	case StatusRejected:
		return "Rejected"
	case StatusWithdrawn:
		return "Withdrawn"
	case StatusCancelled:
		return "Cancelled"
	case StatusFailed:
		return "Failed"
	case LinkUpToDate:
		return "Up to date"
	case LinkBehind:
		return "Behind"
	case LinkDiverged:
		return "Diverged"
	case LinkNotChecked:
		return "Not checked"
	}
	if status == "" {
		return "—"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return inZone(t, "2 Jan 2006")
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func relField(label string, n int) string {
	parts := strings.Split(label, " · ")
	if n < len(parts) {
		return strings.TrimSpace(parts[n])
	}
	return ""
}
