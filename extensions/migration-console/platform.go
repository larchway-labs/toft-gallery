package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The Toft platform API, as this console uses it.
//
// Two callers, one client. The **source** side holds a read-scoped `toftd_` key
// and pulls: apps, deployments, a dependency verdict, a release archive, the
// group directory. The **target** side holds a deploy-scoped key and pushes.
// Nothing here knows which it is; the key decides, and a read-scoped key
// presented at a push route is refused by the platform with a code that says
// so, which is a better place for that boundary than in this app's judgement.
type Platform struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

func newPlatform(baseURL, key string) *Platform {
	return &Platform{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		// Long enough for a release archive on a busy install, short enough
		// that a target which has gone away does not hold a tick open.
		HTTP: &http.Client{Timeout: 3 * time.Minute},
	}
}

// APIError carries the platform's own words. Rendering these rather than a
// generic failure is load-bearing here: the sentence may have come from *either*
// install, and "couldn't reach prod" and "the key was revoked" are different
// problems wearing the same shape.
type APIError struct {
	Status int
	Code   string
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("the platform answered %d", e.Status)
}

func (p *Platform) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return p.HTTP.Do(req)
}

func (p *Platform) getJSON(ctx context.Context, path string, out any) error {
	resp, err := p.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func readAPIError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(raw, &body)
	if body.Error == "" {
		body.Error = strings.TrimSpace(string(raw))
	}
	return &APIError{Status: resp.StatusCode, Code: body.Code, Msg: body.Error}
}

// --- Shapes this console reads ---------------------------------------------

type App struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Visibility    string `json:"visibility"`
	OwnerID       string `json:"owner_id"`
	OwnerGroupID  string `json:"owner_group_id"`
	Framework     string `json:"framework"`
	ExecutionMode string `json:"execution_mode"`
	RepoURL       string `json:"repo_url"`
	Branch        string `json:"branch"`
}

// Deployment is one build, spelled as the platform spells it.
//
// The field names are the platform's wire contract and are worth getting
// exactly right: a mistyped `json:` tag here does not fail — it renders a zero
// value, and a release picker showing "1 Jan 0001" is a picker nobody trusts.
type Deployment struct {
	ID          string     `json:"id"`
	AppID       string     `json:"app_id"`
	Status      string     `json:"status"`
	CommitSHA   string     `json:"commit_sha"`
	Branch      string     `json:"branch"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	TriggeredBy string     `json:"triggered_by"`
	// Current is the release the app is serving.
	Current        bool   `json:"current"`
	RuntimeVersion string `json:"runtime_version"`
	RuntimeLang    string `json:"runtime_language"`
	ExecutionMode  string `json:"execution_mode"`
	// The platform describes a failed build through its log rather than an
	// error field, so that is what the console shows when a push fails.
	BuildLog string `json:"build_log"`
}

// Label is what the release picker shows instead of an id.
//
// `7f3a2c` means nothing to the person pinning it, and a picker that shows ids
// teaches the submitter that they chose an *app* and the release was
// incidental — which is the exact misunderstanding the design exists to
// prevent, and it surfaces later as "why did it deploy the old code?"
func (d Deployment) Label() string {
	parts := []string{}
	if d.CommitSHA != "" {
		sha := d.CommitSHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		if d.Branch != "" {
			parts = append(parts, d.Branch+"@"+sha)
		} else {
			parts = append(parts, sha)
		}
	}
	when := d.StartedAt
	if d.FinishedAt != nil {
		when = *d.FinishedAt
	}
	if !when.IsZero() {
		parts = append(parts, inZone(when, "2 Jan 2006 15:04 MST"))
	}
	if d.TriggeredBy != "" {
		parts = append(parts, "by "+d.TriggeredBy)
	}
	if d.RuntimeVersion != "" {
		parts = append(parts, strings.TrimSpace(d.RuntimeLang+" "+d.RuntimeVersion))
	}
	return strings.Join(parts, " · ")
}

type Group struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Members int    `json:"member_count"`
}

// DependencyReport is what the platform already knows about the packages in a
// release, and it is the one check kind with a producer today.
//
// Four states, and they are not the same thing. **Available false** means the
// feature is off or unlicensed on that install, and `reason` says which.
// **HasData false** means no advisory corpus has ever loaded, which is not a
// clean bill of health and must never render as one. **Scanned false** is where
// most of an estate starts. Only when all three are true does `serving.counts`
// mean anything.
type DependencyReport struct {
	Mode      string `json:"mode"`
	Enforcing bool   `json:"enforcing"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	FeedStale bool   `json:"feed_stale"`
	HasData   bool   `json:"has_data"`
	Scanned   bool   `json:"scanned"`
	Serving   *struct {
		DeploymentID string `json:"deployment_id"`
		Counts       struct {
			Total    int `json:"total"`
			Malware  int `json:"malware"`
			Blocking int `json:"blocking"`
			Counted  int `json:"counted"`
			Unknown  int `json:"unknown"`
		} `json:"counts"`
		Direct int `json:"direct"`
	} `json:"serving"`
	// Refused is the newest deploy the policy turned away, when it is newer
	// than what is serving. It never served, so it is never in the release
	// picker — and this is how the picker can say why the build somebody just
	// pushed is not on the list.
	Refused *struct {
		DeploymentID string `json:"deployment_id"`
		CommitSHA    string `json:"commit_sha"`
		Reason       string `json:"reason"`
	} `json:"refused"`
	// LockedByPolicy is the source app held down by the policy itself rather
	// than by an administrator: what it serves crossed the install's lock
	// threshold, and the way out is a waiver there, not a promotion here.
	LockedByPolicy bool `json:"locked_by_policy"`
}

// Describes reports whether the verdict is about this exact release.
func (r *DependencyReport) Describes(deployID string) bool {
	return r != nil && r.Serving != nil && r.Serving.DeploymentID == deployID
}

// VerdictFor is Verdict for one release, and the only one a request should
// record. The platform scans per app and reports the release that is serving;
// a request may pin any successful release, and borrowing the serving one's
// scan for it would put another release's verdict on the record as this one's.
func (r *DependencyReport) VerdictFor(deployID string) (word, summary string) {
	if r != nil && r.Serving != nil && r.Serving.DeploymentID != deployID {
		return "", "Toft records a verdict only for the release serving on the source, and this isn’t it"
	}
	return r.Verdict()
}

// Verdict collapses the report to one word for a chip, and it distinguishes
// *nothing recorded* from *nothing wrong*. **Not recorded is not clean**, and
// this function is where that stays true: every state that is not a completed
// scan answers with an empty word and a sentence saying why.
func (r *DependencyReport) Verdict() (word, summary string) {
	switch {
	case r == nil:
		return "", "not recorded"
	case !r.Available:
		return "", firstNonEmpty(r.Reason, "dependency policy is off on that install")
	case !r.HasData:
		return "", "no advisory data has loaded on that install, so this isn’t a clean result"
	case !r.Scanned:
		return "", "this release was never scanned"
	case r.Serving == nil:
		return "", "nothing recorded for the release that is serving"
	case r.Serving.Counts.Malware > 0:
		return "vulnerable", fmt.Sprintf("%d flagged as malware", r.Serving.Counts.Malware)
	case r.Serving.Counts.Counted > 0:
		return "warn", fmt.Sprintf("%d of %d findings count on this install",
			r.Serving.Counts.Counted, r.Serving.Counts.Total)
	}
	return "clean", fmt.Sprintf("%d direct dependencies, no findings", r.Serving.Direct)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- Reads ------------------------------------------------------------------

func (p *Platform) Apps(ctx context.Context) ([]App, error) {
	var out []App
	if err := p.getJSON(ctx, "/apps", &out); err != nil {
		// The list route has been an envelope and a bare array at different
		// points in the platform's life; accept both rather than break on a
		// shape this app does not own.
		var env struct {
			Apps []App `json:"apps"`
		}
		if err2 := p.getJSON(ctx, "/apps", &env); err2 == nil {
			return env.Apps, nil
		}
		return nil, err
	}
	return out, nil
}

// appEnvelope is what the single-app reads answer with: the row, plus what the
// platform knows about it. The list route answers bare rows, and the two
// shapes are different on purpose — reading one as the other yields a
// zero-valued app rather than an error, which is the failure this type exists
// to make impossible.
type appEnvelope struct {
	App     App `json:"app"`
	EnvVars []struct {
		Key       string `json:"key"`
		Sensitive bool   `json:"sensitive"`
	} `json:"env_vars"`
}

// AppDetail is one app and the **names** of the environment variables it
// declares. Never the values: the read scope cannot see them and the promotion
// does not want them — configuration on the target is the target's business.
type AppDetail struct {
	App
	EnvKeys []string
	// EnvSensitive is the names the source marks sensitive. The flag travels
	// with the name, so a value the target's administrator fills in later is
	// masked there as it was here.
	EnvSensitive []string
}

func (p *Platform) AppDetail(ctx context.Context, id string) (*AppDetail, error) {
	var env appEnvelope
	if err := p.getJSON(ctx, "/apps/"+url.PathEscape(id), &env); err != nil {
		return nil, err
	}
	out := &AppDetail{App: env.App}
	for _, v := range env.EnvVars {
		out.EnvKeys = append(out.EnvKeys, v.Key)
		if v.Sensitive {
			out.EnvSensitive = append(out.EnvSensitive, v.Key)
		}
	}
	return out, nil
}

func (p *Platform) App(ctx context.Context, id string) (*App, error) {
	d, err := p.AppDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return &d.App, nil
}

func (p *Platform) AppBySlug(ctx context.Context, slug string) (*App, error) {
	var env appEnvelope
	if err := p.getJSON(ctx, "/apps/by-slug/"+url.PathEscape(slug), &env); err != nil {
		return nil, err
	}
	return &env.App, nil
}

// EnvNamesJSON is what POST /apps/upload takes as `env_vars` on a first deploy:
// every name the source app declares, with an **empty value** for the target's
// administrator to fill in.
//
// Names once, values never, and never again: a deploy key may not PATCH an app,
// so no later request can touch them, and configuration drift on the target is
// the target's business. Saying otherwise would be claiming this console
// synchronised something it did not.
func EnvNamesJSON(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	seed := make(map[string]string, len(keys))
	for _, k := range keys {
		seed[k] = ""
	}
	out, err := json.Marshal(seed)
	if err != nil {
		return ""
	}
	return string(out)
}

// EnvSensitiveJSON is `env_sensitive` for the same create: {"NAME": true} for
// each name the source marks sensitive, or "" when none is.
func EnvSensitiveJSON(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	flags := make(map[string]bool, len(keys))
	for _, k := range keys {
		flags[k] = true
	}
	out, err := json.Marshal(flags)
	if err != nil {
		return ""
	}
	return string(out)
}

func (p *Platform) Deployments(ctx context.Context, appID string) ([]Deployment, error) {
	var out []Deployment
	if err := p.getJSON(ctx, "/apps/"+url.PathEscape(appID)+"/deployments", &out); err != nil {
		var env struct {
			Deployments []Deployment `json:"deployments"`
		}
		if err2 := p.getJSON(ctx, "/apps/"+url.PathEscape(appID)+"/deployments", &env); err2 == nil {
			return env.Deployments, nil
		}
		return nil, err
	}
	return out, nil
}

func (p *Platform) Deployment(ctx context.Context, appID, deployID string) (*Deployment, error) {
	var d Deployment
	if err := p.getJSON(ctx,
		"/apps/"+url.PathEscape(appID)+"/deployments/"+url.PathEscape(deployID), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (p *Platform) Dependencies(ctx context.Context, appID string) (*DependencyReport, error) {
	var r DependencyReport
	if err := p.getJSON(ctx, "/apps/"+url.PathEscape(appID)+"/dependencies", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Platform) Groups(ctx context.Context) ([]Group, error) {
	var out []Group
	if err := p.getJSON(ctx, "/groups", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Platform) Group(ctx context.Context, id string) (*Group, error) {
	var env struct {
		Group Group `json:"group"`
	}
	if err := p.getJSON(ctx, "/groups/"+url.PathEscape(id), &env); err != nil {
		return nil, err
	}
	return &env.Group, nil
}

// Archive pulls one release as a ZIP. 410 means the release has been pruned
// from both disk and the artifact store — which is why a request pins a
// deployment rather than an app: a request whose bytes are gone fails loudly
// instead of quietly shipping different ones.
func (p *Platform) Archive(ctx context.Context, appID, deployID string) ([]byte, error) {
	resp, err := p.do(ctx, http.MethodGet,
		"/apps/"+url.PathEscape(appID)+"/deployments/"+url.PathEscape(deployID)+"/archive", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	// Bounded, because this app holds it in memory on its way to the target and
	// the platform's own upload ceiling is 100MB.
	return io.ReadAll(io.LimitReader(resp.Body, 110<<20))
}

// SBOM is one release's CycloneDX document as the platform rendered it, kept
// byte for byte: the console stores it as the record of what a target built,
// and parses it only to compare (promote.go). Bounded, because it is held in
// memory and in the ledger.
func (p *Platform) SBOM(ctx context.Context, appID, deployID string) ([]byte, error) {
	resp, err := p.do(ctx, http.MethodGet,
		"/apps/"+url.PathEscape(appID)+"/deployments/"+url.PathEscape(deployID)+"/sbom", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// Reachable answers whether there is a Toft at this address, without a
// credential. /api/v1/config is the platform's own unauthenticated probe and is
// one of the things a chart may never template, so it is the one endpoint
// guaranteed to be there.
//
// It also answers where that install serves apps, from the same response:
// `apps_base_url` (empty on a single-origin install, meaning this address) and
// `apps_path_prefix`. "" when the install did not say.
func (p *Platform) Reachable(ctx context.Context) (appsURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/v1/config", nil)
	if err != nil {
		return "", err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", readAPIError(resp)
	}
	var cfg struct {
		AppsBaseURL    string `json:"apps_base_url"`
		AppsPathPrefix string `json:"apps_path_prefix"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&cfg) != nil {
		return "", nil
	}
	return appsURLFrom(p.BaseURL, cfg.AppsBaseURL, cfg.AppsPathPrefix), nil
}

// appsURLFrom joins an install's apps origin and path prefix into one address
// ending in a slash. A base that is not http(s) is refused rather than linked.
func appsURLFrom(baseURL, appsBase, prefix string) string {
	base := strings.TrimRight(strings.TrimSpace(appsBase), "/")
	if base == "" {
		base = strings.TrimRight(baseURL, "/")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	if prefix == "" {
		prefix = "/apps/"
	}
	return base + "/" + strings.Trim(prefix, "/") + "/"
}

// --- Pushes -----------------------------------------------------------------

// UploadResult is what both push routes answer with, flattened to what this app
// records: the app it landed on and the deployment watching it.
type UploadResult struct {
	AppID        string
	AppSlug      string
	DeploymentID string
}

func bundleForm(fields map[string]string, filename string, zipBytes []byte) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v == "" {
			continue
		}
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(zipBytes); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

// CreateFromArchive is the first deploy of a pair: the app does not exist on the
// target yet.
//
// owner_group_id is not optional and the platform says so — a deploy key
// belongs to nobody, so without a group the app would have no owner at all.
// `env_vars` seeds the *names* the source app declares, with empty values for
// the target's administrator to fill in; a deploy key may not PATCH the app, so
// no later request can touch them, and configuration drift on the target is the
// target's business.
//
// The slug is sent **exact**. Without `slug_exact` the platform treats a slug
// as a proposal and lands a taken one on `<slug>-2` — on a busy target that is
// the approver's chosen name quietly becoming somebody else's, while the ledger
// records the one they chose. Exact, a taken slug is refused with `name_taken`
// and the request fails saying so.
func (p *Platform) CreateFromArchive(ctx context.Context, zipBytes []byte, name, slug, ownerGroupID, visibility, envVarsJSON, envSensitiveJSON string) (*UploadResult, error) {
	body, ct, err := bundleForm(map[string]string{
		"name":           name,
		"slug":           slug,
		"slug_exact":     "true",
		"owner_group_id": ownerGroupID,
		"visibility":     visibility,
		"env_vars":       envVarsJSON,
		"env_sensitive":  envSensitiveJSON,
	}, slug+".zip", zipBytes)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, "/apps/upload", body, ct)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	var out struct {
		App        App        `json:"app"`
		Deployment Deployment `json:"deployment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &UploadResult{AppID: out.App.ID, AppSlug: out.App.Slug, DeploymentID: out.Deployment.ID}, nil
}

// PushVersion is every later deploy of a pair. Slug and visibility ride with the
// bundle, because a promotion decides what the destination is called and who
// may see it as much as it decides what code it runs.
func (p *Platform) PushVersion(ctx context.Context, appID string, zipBytes []byte, slug, visibility string) (*UploadResult, error) {
	body, ct, err := bundleForm(map[string]string{
		"slug":       slug,
		"visibility": visibility,
	}, "bundle.zip", zipBytes)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, http.MethodPost, "/apps/"+url.PathEscape(appID)+"/upload", body, ct)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	var d Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	return &UploadResult{AppID: appID, DeploymentID: d.ID}, nil
}
