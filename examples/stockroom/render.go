package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Flash is the one-shot message after a redirect.
//
// Every mutation in this app redirects and every redirect carries one of these:
// a form that saves and re-renders in place leaves the browser holding a POST,
// and the reload somebody eventually presses re-submits it.
type Flash struct {
	Tone string // ok | warn | bad
	Text string
}

func setFlash(w http.ResponseWriter, tone, text string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "stockroom_flash",
		Value:    url.QueryEscape(tone + "|" + text),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30,
	})
}

func takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie("stockroom_flash")
	if err != nil || c.Value == "" {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: "stockroom_flash", Value: "", Path: "/", MaxAge: -1})
	raw, err := url.QueryUnescape(c.Value)
	if err != nil {
		return nil
	}
	tone, text, ok := strings.Cut(raw, "|")
	if !ok {
		return nil
	}
	return &Flash{Tone: tone, Text: text}
}

// actorFrom names who is acting, from the only thing Toft tells an app
// about its viewer: an opaque per-session id.
//
// Not a person's name, and it must never be dressed up as one. The platform
// deliberately withholds identity from apps, so "session 4f2a" is the whole
// truth — enough to tell two people's edits apart in the ledger, and not enough
// to claim it knows who either of them is.
func actorFrom(r *http.Request) string {
	if id := r.Header.Get("X-Toft-Session"); id != "" {
		if len(id) > 4 {
			id = id[:4]
		}
		return "session " + id
	}
	if r.Header.Get("X-Toft-Cron") != "" {
		return "the schedule"
	}
	return "signed in locally"
}

// The five aisles, in the gallery's five-series order and no sixth. Assigned by
// entity and never by rank: a filter that hides one aisle must not repaint the
// others. The hexes are `--lp-c1`…`--lp-c5` from toft-kit.css, copied
// because a Go map cannot read a stylesheet — TestAisleColoursMatchTheKit holds
// them together.
var catColors = map[string]string{
	"Electronics": "#4338CA",
	"Consumables": "#0E9AA7",
	"Tools":       "#2E9E63",
	"Safety":      "#8B5CF6",
	"Spares":      "#C2703B",
}

var stateLabels = map[string][2]string{
	// state → {label, tone}
	"ok":       {"On the shelf", "ok"},
	"low":      {"At reorder point", "warn"},
	"out":      {"Out of stock", "bad"},
	"on_order": {"On order", "info"},
	"retired":  {"Retired", "muted"},
}

func money(cents int) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := cents / 100
	var b strings.Builder
	s := fmt.Sprintf("%d", whole)
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s$%s.%02d", sign, b.String(), cents%100)
}

// moneyShort is for the places where the exact cent is noise — a tile, an axis,
// a band label. The full figure is always available on the row it summarises.
func moneyShort(cents int) string {
	switch v := float64(cents) / 100; {
	case v >= 100000:
		return fmt.Sprintf("$%.0fk", v/1000)
	case v >= 1000:
		return fmt.Sprintf("$%.1fk", v/1000)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2 Jan 2006")
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"url":        s.cfg.url,
		"money":      money,
		"moneyShort": moneyShort,
		"relTime":    relTime,
		"fmtTime":    func(t time.Time, layout string) string { return t.Local().Format(layout) },
		"catColor": func(c string) string {
			if v, ok := catColors[c]; ok {
				return v
			}
			return "#71717A" // --lp-neu-solid
		},
		"stateLabel": func(state string) string {
			if v, ok := stateLabels[state]; ok {
				return v[0]
			}
			return state
		},
		"stateTone": func(state string) string {
			if v, ok := stateLabels[state]; ok {
				return v[1]
			}
			return "muted"
		},
		"signed": func(n int) string { return fmt.Sprintf("%+d", n) },
		"pct": func(part, whole int) float64 {
			if whole == 0 {
				return 0
			}
			return float64(part) / float64(whole) * 100
		},
		"add":  func(a, b int) int { return a + b },
		"sub":  func(a, b int) int { return a - b },
		"divf": func(a, b int) float64 { return float64(a) / float64(b) },
		"has":  contains,
		// dict is what lets a partial take named arguments. Go templates pass
		// one value, and a partial that took a positional list would be
		// unreadable at every call site three months from now.
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict takes name/value pairs, got %d values", len(pairs))
			}
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings, got %T", pairs[i])
				}
				out[key] = pairs[i+1]
			}
			return out, nil
		},
		"categories": func() []string { return Categories },
		"kinds":      func() []string { return MovementKinds },
		"reports":    func() []ReportKind { return ReportKinds },
		"title":      func(s string) string { return strings.ToUpper(s[:1]) + s[1:] },
		"replace":    strings.ReplaceAll,
		"query": func(base string, pairs ...string) string {
			v := url.Values{}
			for i := 0; i+1 < len(pairs); i += 2 {
				if pairs[i+1] != "" {
					v.Set(pairs[i], pairs[i+1])
				}
			}
			if len(v) == 0 {
				return s.cfg.url(base)
			}
			return s.cfg.url(base) + "?" + v.Encode()
		},
	}
}
