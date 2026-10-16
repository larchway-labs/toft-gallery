package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// What the platform tells this app about the person in front of it, and the two
// questions those answers are for.
//
//   - **X-Toft-Role** decides *configure vs submit*. It is signed, arrives
//     on every request, needs no switch, and names nobody. So the console's
//     configuration surface is safe before an administrator has finished
//     installing anything.
//   - **GET /api/v1/app/viewer** decides *who this is*, and only where an
//     administrator switched viewer identity on. It is what puts a name on a
//     ledger row and an address on a notification.
//
// The console never acts as the person in front of it. It holds no credential
// of theirs and is offered none: the viewer's identity is **input, never
// authority**, and every call it makes to a platform API is made with a key an
// administrator pasted into it.

const (
	RoleAnonymous = "anonymous"
	RoleViewer    = "viewer"
	RoleEditor    = "editor"
	RoleOwner     = "owner"
	RoleSystem    = "system"
	RoleApp       = "app"
	// RoleUnknown is what an absent header, an absent key, a bad MAC and an
	// expired blob all read as. One safe answer for four failures, and it is
	// never privileged: every check here is a comparison, so a missing header
	// refuses, because the one deployment where it is missing is the one nobody
	// configured.
	RoleUnknown = "unknown"
)

var mintedRoles = []string{RoleAnonymous, RoleViewer, RoleEditor, RoleOwner, RoleSystem, RoleApp}

// verifyRole mirrors the platform's own computation exactly (internal/auth/
// rolesig.go). It is an HMAC over a dot-delimited payload, checked in constant
// time before anything is parsed — structure decoded ahead of authentication is
// how a verifier grows a second entry point.
func verifyRole(key, blob, appID string, now time.Time) string {
	if key == "" || blob == "" {
		return RoleUnknown
	}
	idx := strings.LastIndexByte(blob, '.')
	if idx < 0 {
		return RoleUnknown
	}
	payload, sig := blob[:idx], blob[idx+1:]
	if !hmac.Equal([]byte(sig), []byte(roleSignature(key, payload))) {
		return RoleUnknown
	}
	fields := strings.Split(payload, ".")
	if len(fields) != 4 || fields[0] != "v1" {
		return RoleUnknown
	}
	signedApp, err := base64.RawURLEncoding.DecodeString(fields[2])
	if err != nil || (appID != "" && string(signedApp) != appID) {
		return RoleUnknown
	}
	unix, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || !time.Unix(unix, 0).UTC().After(now.UTC()) {
		return RoleUnknown
	}
	for _, known := range mintedRoles {
		if fields[1] == known {
			return known
		}
	}
	return RoleUnknown
}

func roleSignature(key, payload string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("toft-role\x00"))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Viewer is GET /api/v1/app/viewer's answer.
type Viewer struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	IsAdmin bool   `json:"is_admin"`
	Groups  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"groups"`
}

// ErrIdentityOff is 403 viewer_identity_disabled: an administrator has not
// finished installing this — **not** that the person is signed out. The platform
// requires those two never collapse, and here the difference is the difference
// between "ask your administrator to finish the install" and a sign-in page that
// would loop.
var ErrIdentityOff = errors.New("viewer identity is not enabled for this app")

// ErrNoViewer is the 204: there is a request but nobody in particular to
// describe. Legitimate, and not an error the person can act on.
var ErrNoViewer = errors.New("no viewer")

// fetchViewer relays the act-for blob the platform put on the request. The
// console asserts nothing about who the viewer is; it passes through what it was
// given and lets the platform resolve it.
func (a *Console) fetchViewer(ctx context.Context, r *http.Request) (*Viewer, error) {
	if a.cfg.AppToken == "" || a.cfg.APIURL == "" {
		return nil, ErrIdentityOff
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.APIURL+"/api/v1/app/viewer", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AppToken)
	if blob := r.Header.Get("X-Toft-Act-For"); blob != "" {
		req.Header.Set("X-Toft-Act-For", blob)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, ErrNoViewer
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrIdentityOff
	case resp.StatusCode >= 300:
		return nil, readAPIError(resp)
	}
	var v Viewer
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Capabilities is GET /api/v1/app/self's `capabilities`, which is how the
// console knows whether to offer notification settings at all. Absent, not off:
// when `email` is not among them the settings are **not in the interface**, not
// greyed with a tooltip and not a switch that fails on save.
func (a *Console) capabilities(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if a.cfg.AppToken == "" || a.cfg.APIURL == "" {
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.APIURL+"/api/v1/app/self", nil)
	if err != nil {
		return out
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AppToken)
	resp, err := a.http.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return out
	}
	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return out
	}
	for _, c := range body.Capabilities {
		out[c] = true
		if i := strings.IndexByte(c, '.'); i > 0 {
			out[c[:i]] = true
		}
	}
	return out
}

// Caller is everything one request's authority resolves to, in one struct, so a
// handler asks a question rather than re-deriving an answer.
type Caller struct {
	Role   string
	Viewer *Viewer
	// IdentityOff means an administrator has not switched viewer identity on.
	// Distinct from "no viewer", and the copy differs.
	IdentityOff bool

	// Editor may write targets, the roster, the links and the settings. It is
	// the platform's `owner` or `editor` on this app — which a Toft
	// administrator arrives with, because Authorize hands them effective
	// ownership of every object.
	Editor bool
	// Reviewer may approve and reject. The roster's, not the platform's: an app
	// that could not refuse an administrator in its own domain would have no
	// domain, so `is_admin` grants nothing here.
	Reviewer bool
	// Submitter may raise a request. Every signed-in person past the gateway.
	Submitter bool
}

func (c Caller) Name() string {
	if c.Viewer != nil && c.Viewer.Name != "" {
		return c.Viewer.Name
	}
	if c.Viewer != nil {
		return c.Viewer.ID
	}
	return "somebody"
}

func (c Caller) ID() string {
	if c.Viewer != nil {
		return c.Viewer.ID
	}
	return ""
}

func (c Caller) Email() string {
	if c.Viewer != nil {
		return c.Viewer.Email
	}
	return ""
}

// GroupIDs is the viewer's group list, which is what "an app I own" is resolved
// against — never a re-derivation of Toft's grant ladder.
func (c Caller) GroupIDs() map[string]bool {
	out := map[string]bool{}
	if c.Viewer == nil {
		return out
	}
	for _, g := range c.Viewer.Groups {
		out[g.ID] = true
	}
	return out
}

// resolveCaller answers the two questions once per request.
func (a *Console) resolveCaller(r *http.Request) Caller {
	c := Caller{Role: verifyRole(a.cfg.RoleKey, r.Header.Get("X-Toft-Role"), a.cfg.AppID, time.Now())}
	c.Editor = c.Role == RoleOwner || c.Role == RoleEditor
	c.Submitter = c.Editor || c.Role == RoleViewer

	if !c.Submitter {
		return c
	}
	v, err := a.fetchViewer(r.Context(), r)
	switch {
	case errors.Is(err, ErrIdentityOff):
		c.IdentityOff = true
		return c
	case err != nil:
		return c
	}
	c.Viewer = v
	c.Reviewer = a.isReviewer(r.Context(), c)
	return c
}

// isReviewer reads the roster. A row names a Toft user id, or a group id so
// that a team is one row.
func (a *Console) isReviewer(ctx context.Context, c Caller) bool {
	if c.Viewer == nil {
		return false
	}
	roster, err := a.store.ListReviewers(ctx)
	if err != nil {
		return false
	}
	groups := c.GroupIDs()
	for _, r := range roster {
		if r.SubjectType == "user" && r.SubjectID == c.Viewer.ID {
			return true
		}
		if r.SubjectType == "group" && groups[r.SubjectID] {
			return true
		}
	}
	return false
}
