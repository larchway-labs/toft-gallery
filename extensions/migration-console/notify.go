package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Telling people, through the platform, and never load-bearing.
//
// Mail goes out through POST /api/v1/app/notify/email — the app-facing
// integration route, gated by the platform's four gates, which means an
// administrator has attached an email connection to this app. That is the last
// install action and it is the only optional one.
//
// **A send never fails the thing it is about.** No notification blocks or fails
// a migration; a failed send becomes a ledger row saying the mail did not go
// and why, and the migration's outcome is what it was. That is why mail is a
// leaf of this design rather than a step in it, and why every function here
// returns nothing.

func (c *Console) sendMail(ctx context.Context, to []string, subject, text string) error {
	// The web app sends as itself. The tick job has no app token and sends
	// with its run's, which the platform takes on this route and no other.
	token := c.cfg.AppToken
	if token == "" {
		token = c.cfg.JobToken
	}
	if len(to) == 0 || token == "" || c.cfg.APIURL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"to": to, "subject": subject, "text": text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.APIURL+"/api/v1/app/notify/email", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	return nil
}

// notifyRequester tells the person who raised it, if they asked to be told and
// if this install can send at all.
//
// The address comes from the viewer call, which **withholds it entirely on a
// public app** — so this console is `private` or `authenticated`, and /health
// says so rather than letting the first notification discover it.
func (c *Console) notifyRequester(ctx context.Context, req *Request, subject, text string) {
	if !req.NotifyRequester || req.RequesterEmail == "" {
		return
	}
	c.deliver(ctx, req, []string{req.RequesterEmail}, subject, text, "requester")
}

// notifyFailure reaches the administrators' group named in settings, by the
// group's **own address**. Settings refuses to save a group that has none, for
// the reason stated there.
func (c *Console) notifyFailure(ctx context.Context, req *Request, subject, text string) {
	c.notifyGroupAbout(ctx, req, subject, text)
}

func (c *Console) notifyGroup(ctx context.Context, subject, text string) {
	c.notifyGroupAbout(ctx, nil, subject, text)
}

func (c *Console) notifyGroupAbout(ctx context.Context, req *Request, subject, text string) {
	settings, err := c.store.Settings(ctx)
	if err != nil {
		return
	}
	addr := strings.TrimSpace(settings[SettingAdminGroupEmail])
	if addr == "" {
		return
	}
	c.deliver(ctx, req, []string{addr}, subject, text, "administrators group")
}

func (c *Console) deliver(ctx context.Context, req *Request, to []string, subject, text, audience string) {
	// Detached from the request that triggered it, and bounded: a send must
	// never block or fail the thing it is about.
	c.sends.Add(1)
	go func() {
		defer c.sends.Done()
		sendCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		ev := Event{Action: "notification.sent", ActorName: "the console",
			Detail: audience + ": " + subject}
		if req != nil {
			ev.RequestID, ev.SourceAppID, ev.TargetID = req.ID, req.SourceAppID, req.TargetID
		}
		if err := c.sendMail(sendCtx, to, subject, text); err != nil {
			ev.Action = "notification.failed"
			ev.Detail = audience + ": " + subject + " — " + err.Error()
			log.Printf("notification: %v", err)
		}
		if err := c.store.WriteEvent(sendCtx, ev); err != nil {
			log.Printf("recording a notification: %v", err)
		}
	}()
}

// --- Small shared helpers ---------------------------------------------------

// newID is this app's own id, and it is not a UUID library: a random 128-bit
// hex string is the same thing for this purpose and is one fewer dependency in
// a repository whose whole point is that `go build` is the build.
func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A process that cannot read randomness cannot mint an id, and an id
		// collision here would attach one person's document to another
		// person's request.
		panic("no randomness available: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// maxSlugLen is the platform's own ceiling: a release is a Kubernetes object
// named slug, a dash and eight characters of the deployment, inside a 63
// character DNS label. The console states its slug exactly (slug_exact), so a
// longer one is refused at the push rather than cut — cutting it here keeps the
// name the approver reads the name the app will answer on.
const maxSlugLen = 63 - len("-") - 8

// sanitizeSlug mirrors the platform's own rule closely enough that what this
// app shows and what the platform stores agree. The platform sanitizes again on
// the way in, which is the authority; this is so the approver reads the name
// the app will actually answer on rather than the one they typed.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > maxSlugLen {
		s = strings.Trim(s[:maxSlugLen], "-")
	}
	return s
}
