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
	"os"
	"strconv"
	"strings"
	"time"
)

// The Toft app API, spoken directly.
//
// There is a Go SDK; this example does not use it, so that what an SDK does is
// on the page — about two hundred lines, no dependencies. Everything it needs
// arrives in the process environment: TOFT_API_URL and TOFT_APP_TOKEN in the
// web process, injected at deploy and rotated by the next one, and
// TOFT_JOB_TOKEN in place of the second inside the report job.
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
	// job is set inside the report job, where the credential is the run's
	// rather than the app's: good for the send routes and nothing else.
	job bool
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

// newJobPlatform is the same client inside the report job, carrying the run's
// token. There is no app token in a job's environment, by design: a run that
// held one could start more runs, and one job could fan out into many.
func newJobPlatform(c Config) *Platform {
	token := os.Getenv("TOFT_JOB_TOKEN")
	if c.APIURL == "" || token == "" {
		return nil
	}
	return &Platform{baseURL: c.APIURL, token: token, job: true,
		http: &http.Client{Timeout: 60 * time.Second}}
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
	if p != nil && p.job {
		// Refused for a run token, so not asked. `deliver` reads a Self error
		// as "unknown" and tries the richer send first, letting the send
		// route's own refusal decide — which is the answer that counts anyway.
		return nil, errors.New("a job reads nothing about its app; it may only send")
	}
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

// JobRun is one run of this app's report job, as the app is allowed to see it.
// There is no actor here by design: `started_by` says *how* a run started, and
// `viewer` names nobody.
type JobRun struct {
	ID              string          `json:"id"`
	Job             string          `json:"job"`
	Status          string          `json:"status"`
	StartedBy       string          `json:"started_by"`
	Params          json.RawMessage `json:"params"`
	ScheduledFor    *time.Time      `json:"scheduled_for"`
	QueuedAt        time.Time       `json:"queued_at"`
	StartedAt       *time.Time      `json:"started_at"`
	FinishedAt      *time.Time      `json:"finished_at"`
	Progress        int             `json:"progress"`
	ProgressMessage string          `json:"progress_message"`
	QueuePosition   int             `json:"queue_position"`
	FailureReason   string          `json:"failure_reason"`
	StatusURL       string          `json:"status_url"`
	Deduplicated    bool            `json:"deduplicated"`
}

// Provenance is the one thing the run list has to get right: a run with no
// person behind it must never render as one.
func (r JobRun) Provenance() string {
	switch r.StartedBy {
	case "viewer":
		return "someone on this app"
	case "schedule":
		return "the schedule"
	case "cli":
		return "the CLI"
	case "ui":
		return "Toft's Automation tab"
	}
	return "the app itself"
}

// Tone is the chip a status wears: six ways to end, two to be open.
func (r JobRun) Tone() string {
	switch r.Status {
	case "succeeded":
		return "ok"
	case "running", "queued", "claimed":
		return "info"
	case "skipped", "cancelled":
		return "warn"
	}
	return "bad"
}

// Took is how long the run ran, or "" while it has not finished.
func (r JobRun) Took() string {
	if r.StartedAt == nil || r.FinishedAt == nil {
		return ""
	}
	return r.FinishedAt.Sub(*r.StartedAt).Round(time.Second).String()
}

// ParamsText is the params as the run recorded them, for one line of a list.
func (r JobRun) ParamsText() string {
	var m map[string]any
	if json.Unmarshal(r.Params, &m) != nil || len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for _, k := range []string{"report", "channel", "to"} {
		if v, ok := m[k]; ok && fmt.Sprint(v) != "" {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

// JobRuns lists the report job's runs, newest first — every one, as the app.
func (p *Platform) JobRuns(ctx context.Context, job string, limit int) ([]JobRun, error) {
	var out struct {
		Runs []JobRun `json:"runs"`
	}
	err := p.do(ctx, http.MethodGet, "/api/v1/app/job-runs?scope=app&job="+url.QueryEscape(job)+
		"&limit="+strconv.Itoa(limit), nil, "", nil, &out)
	return out.Runs, err
}

// StartJob queues a run of one of this app's jobs and returns at once.
//
// actFor is the X-Toft-Act-For header the request arrived with, relayed
// untouched: with it the run is attributed to the person who pressed the
// button, without it to the app. The app cannot mint, read or alter it — it
// only hands it back. Params are checked against `toft.toml` at the door, so a
// value outside a declared choice is a 400 naming it, not a failed run.
func (p *Platform) StartJob(ctx context.Context, job string, params map[string]string, actFor string) (*JobRun, error) {
	body, _ := json.Marshal(map[string]any{"params": params})
	var headers map[string]string
	if actFor != "" {
		headers = map[string]string{"X-Toft-Act-For": actFor}
	}
	var run JobRun
	err := p.do(ctx, http.MethodPost, "/api/v1/app/jobs/"+url.PathEscape(job)+"/runs",
		bytes.NewReader(body), "application/json", headers, &run)
	return &run, err
}

func idempotency(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Idempotency-Key": key}
}
