package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The Toft app API, spoken directly.
//
// There is a Node SDK and a Python SDK; there is no Go one, so this file is it
// — about two hundred lines, no dependencies. Everything it needs arrives in
// the process environment: TOFT_API_URL and TOFT_APP_TOKEN, injected
// at deploy and rotated by the next one.
//
// Two rules run through the whole surface and are worth stating once:
//
//   - The app supplies the message; the platform supplies the identity. There
//     is no channel argument, no `from`, no `reply_to`. Sending one is a 400
//     that names the rule, not a field quietly dropped.
//   - The record, not the message, is what a workflow may rely on. A send is
//     accepted with a 202 and a send id; delivery happens afterwards, and the
//     app's own deliveries table is what the Reports tab reads.
type Platform struct {
	baseURL string
	token   string
	http    *http.Client
}

// contractHeader pins the app-API version this code was written against. If the
// platform ever moves past it the call fails with a message naming the skew,
// which is the loud failure the header exists to produce — far better than a
// field silently changing meaning under a running app.
const (
	contractHeader  = "X-Toft-App-Contract"
	contractVersion = "1"
)

func newPlatform(c Config) *Platform {
	if c.APIURL == "" || c.AppToken == "" {
		return nil // Not deployed on Toft. Absent, not broken.
	}
	return &Platform{
		baseURL: c.APIURL,
		token:   c.AppToken,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Available reports whether this process is running inside Toft at all.
// Every surface that offers a send checks it first and explains its absence,
// rather than presenting a button that cannot work.
func (p *Platform) Available() bool { return p != nil }

// Self is what the app is told about itself.
type Self struct {
	ID           string          `json:"id"`
	Slug         string          `json:"slug"`
	Name         string          `json:"name"`
	Mode         string          `json:"mode"`
	Capabilities []string        `json:"capabilities"`
	Policy       json.RawMessage `json:"policy"`
}

func (s *Self) Can(capability string) bool {
	if s == nil {
		return false
	}
	for _, c := range s.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// APIError carries the platform's own refusal text.
//
// Kept whole rather than flattened to a string: these messages are written to
// be shown to a person and name the remedy in the administrator's words ("ask
// an administrator to attach this app to Slack"), which is exactly what the
// Reports tab should display instead of "403".
type APIError struct {
	Status  int
	Message string `json:"error"`
	Code    string `json:"code"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("the platform answered %d", e.Status)
}

func (p *Platform) do(ctx context.Context, method, path string, body io.Reader,
	contentType string, headers map[string]string, out any) error {
	if p == nil {
		return errors.New("this app is not running on Toft, so it cannot send anything")
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set(contractHeader, contractVersion)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(payload, apiErr)
		return apiErr
	}
	if out != nil && len(payload) > 0 {
		return json.Unmarshal(payload, out)
	}
	return nil
}

func (p *Platform) Self(ctx context.Context) (*Self, error) {
	var s Self
	if err := p.do(ctx, http.MethodGet, "/api/v1/app/self", nil, "", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SendResult is the receipt. send_id is the ledger row an administrator can
// look up in the install-wide Integration log.
type SendResult struct {
	SendID string `json:"send_id"`
	Status string `json:"status"`
}

// PostSlack posts one message to the channel this app's owners chose.
//
// No channel argument exists, deliberately: routing is the app owners' decision,
// made on the app's Integrations tab from the channels the bot was invited into.
func (p *Platform) PostSlack(ctx context.Context, text, idempotencyKey string) (*SendResult, error) {
	body, _ := json.Marshal(map[string]string{"text": text})
	var out SendResult
	err := p.do(ctx, http.MethodPost, "/api/v1/app/notify/slack",
		bytes.NewReader(body), "application/json", idempotency(idempotencyKey), &out)
	return &out, err
}

// PostSlackFile uploads the PDF itself, so the report lands in the channel as
// something somebody can open rather than as a paragraph describing it.
func (p *Platform) PostSlackFile(ctx context.Context, filename string, content []byte,
	title, comment, idempotencyKey string) (*SendResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)},
		"Content-Type":        {"application/pdf"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(content); err != nil {
		return nil, err
	}
	_ = mw.WriteField("title", title)
	_ = mw.WriteField("comment", comment)
	if err := mw.Close(); err != nil {
		return nil, err
	}

	var out SendResult
	err = p.do(ctx, http.MethodPost, "/api/v1/app/notify/slack/file",
		&buf, mw.FormDataContentType(), idempotency(idempotencyKey), &out)
	return &out, err
}

// EmailRequest is the half of a message an app is allowed to choose.
//
// Recipients are here and the sender is not, which is the one asymmetry in this
// API worth understanding: the use case is a report to people who have no
// Toft account, so an app picks who receives it — and every recipient is
// written to the install's ledger, which is the only control over app email.
type EmailRequest struct {
	To      []string `json:"to,omitempty"`
	CC      []string `json:"cc,omitempty"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html,omitempty"`
}

// SendEmail sends the report, with the PDF attached when one is given.
func (p *Platform) SendEmail(ctx context.Context, req EmailRequest, filename string,
	attachment []byte, idempotencyKey string) (*SendResult, error) {
	payload, _ := json.Marshal(req)
	var out SendResult

	if len(attachment) == 0 {
		err := p.do(ctx, http.MethodPost, "/api/v1/app/notify/email",
			bytes.NewReader(payload), "application/json", idempotency(idempotencyKey), &out)
		return &out, err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("payload", string(payload))
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="attachment"; filename=%q`, filename)},
		"Content-Type":        {"application/pdf"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(attachment); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	err = p.do(ctx, http.MethodPost, "/api/v1/app/notify/email",
		&buf, mw.FormDataContentType(), idempotency(idempotencyKey), &out)
	return &out, err
}

// CronRun is one of this app's scheduled runs, as the app is allowed to see it.
// There is no actor here by design: an app learns *that* a viewer started a run,
// never which one.
type CronRun struct {
	ID              string  `json:"id"`
	Schedule        string  `json:"schedule"`
	Status          string  `json:"status"`
	HTTPStatus      *int    `json:"http_status"`
	Error           string  `json:"error"`
	Result          string  `json:"result"`
	DurationMS      int64   `json:"duration_ms"`
	StartedByViewer bool    `json:"started_by_viewer"`
	Scheduled       bool    `json:"scheduled"`
	StartedAt       string  `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
}

// Provenance is the one thing the run list has to get right: a run with no
// resolved actor must never render as a person.
func (r CronRun) Provenance() string {
	switch {
	case r.StartedByViewer:
		return "someone on this app"
	case r.Scheduled:
		return "the schedule"
	}
	return "the app itself"
}

func (p *Platform) Runs(ctx context.Context, limit int) ([]CronRun, error) {
	var out struct {
		Runs []CronRun `json:"runs"`
	}
	err := p.do(ctx, http.MethodGet, "/api/v1/app/runs?limit="+strconv.Itoa(limit), nil, "", nil, &out)
	return out.Runs, err
}

// TriggerSchedule fires one of this app's scheduled tasks by name, now.
//
// The app supplies parameters and nothing else — no path, no verb, no schedule.
// That is what keeps a shell out of this subsystem entirely.
func (p *Platform) TriggerSchedule(ctx context.Context, name string, params map[string]string) error {
	body, _ := json.Marshal(map[string]any{"params": params})
	return p.do(ctx, http.MethodPost,
		"/api/v1/app/schedules/"+url.PathEscape(name)+"/run",
		bytes.NewReader(body), "application/json", nil, nil)
}

// TaskLog pushes lines into a scheduled run's own log.
//
// The platform captures nothing here and cannot: it made an HTTP request, and
// this task's prints land in the app's process log interleaved with every other
// request being served. Pushed lines are what remains readable if the run is
// killed at the timeout — which is the run somebody actually needs a log for.
//
// Every failure is swallowed. Logging that could take down the thing it observes
// is worse than no logging.
func (p *Platform) TaskLog(ctx context.Context, runID string, lines ...string) {
	if p == nil || runID == "" || len(lines) == 0 {
		return
	}
	type line struct {
		Line   string `json:"line"`
		Stream string `json:"stream"`
	}
	payload := struct {
		Lines []line `json:"lines"`
	}{}
	for _, l := range lines {
		stream := "stdout"
		if strings.HasPrefix(l, "!") {
			stream, l = "stderr", strings.TrimSpace(strings.TrimPrefix(l, "!"))
		}
		payload.Lines = append(payload.Lines, line{Line: l, Stream: stream})
	}
	body, _ := json.Marshal(payload)
	_ = p.do(ctx, http.MethodPost, "/api/v1/app/runs/"+url.PathEscape(runID)+"/logs",
		bytes.NewReader(body), "application/json", nil, nil)
}

func idempotency(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Idempotency-Key": key}
}
