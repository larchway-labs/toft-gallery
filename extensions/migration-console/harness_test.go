package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The flow tests run the console against a real Postgres and three fake Toft
// installs — the source, Stage and Prod — plus a fake of the install the
// console runs on, which answers who the viewer is.
//
// They need MC_TEST_DATABASE_URL, any database the test may create schemas in
// (dev's is postgres://toft:toft@127.0.0.1:5432/toft?sslmode=disable). Each
// test gets a schema of its own, dropped afterwards. Without the variable they
// skip, saying so, rather than pass.

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MC_TEST_DATABASE_URL is not set, so the flow tests have no database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "mc_test_" + randHex(6)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	// Twice, as every boot after the first does.
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		t.Fatalf("the schema is not idempotent: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Store{pool: pool}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- A fake Toft --------------------------------------------------------------

type fakeInstall struct {
	t    *testing.T
	name string
	srv  *httptest.Server

	mu       sync.Mutex
	apps     map[string]*App
	deploys  map[string][]*Deployment // newest first
	archives map[string][]byte        // by deployment
	sboms    map[string]string        // by deployment
	deps     map[string]*DependencyReport
	groups   []Group

	archiveHits int
	received    [][]byte // every zip pushed here, in order
	nextSBOM    string   // the SBOM the next build here records
}

func newFakeInstall(t *testing.T, name string) *fakeInstall {
	f := &fakeInstall{t: t, name: name,
		apps: map[string]*App{}, deploys: map[string][]*Deployment{}, archives: map[string][]byte{},
		sboms: map[string]string{}, deps: map[string]*DependencyReport{},
		groups: []Group{{ID: "g-" + name, Name: name + " owners", Members: 1}}}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	notFound := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}
	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]string{}) })
	mux.HandleFunc("GET /api/v1/groups", func(w http.ResponseWriter, r *http.Request) { j(w, f.groups) })
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []App{}
		for _, a := range f.apps {
			out = append(out, *a)
		}
		j(w, out)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a := f.apps[r.PathValue("id")]
		if a == nil {
			notFound(w)
			return
		}
		j(w, map[string]any{"app": a, "env_vars": []any{}})
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/deployments", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []Deployment{}
		for _, d := range f.deploys[r.PathValue("id")] {
			out = append(out, *d)
		}
		j(w, out)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/deployments/{d}", func(w http.ResponseWriter, r *http.Request) {
		if d := f.deployment(r.PathValue("id"), r.PathValue("d")); d != nil {
			j(w, d)
			return
		}
		notFound(w)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/deployments/{d}/archive", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.archiveHits++
		b, ok := f.archives[r.PathValue("d")]
		if !ok {
			w.WriteHeader(http.StatusGone)
			_, _ = io.WriteString(w, `{"error":"this release has been pruned"}`)
			return
		}
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/deployments/{d}/sbom", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		s, ok := f.sboms[r.PathValue("d")]
		if !ok {
			notFound(w)
			return
		}
		_, _ = io.WriteString(w, s)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/dependencies", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if rep := f.deps[r.PathValue("id")]; rep != nil {
			j(w, rep)
			return
		}
		j(w, DependencyReport{Mode: "off", Reason: "Dependency policy is off on this install."})
	})
	mux.HandleFunc("POST /api/v1/apps/upload", func(w http.ResponseWriter, r *http.Request) {
		body := f.readZip(r)
		id := "app-" + randHex(4)
		a := &App{ID: id, Slug: r.FormValue("slug"), Name: r.FormValue("name"), OwnerGroupID: r.FormValue("owner_group_id")}
		f.mu.Lock()
		f.apps[id] = a
		f.mu.Unlock()
		d := f.build(id, body)
		j(w, map[string]any{"app": a, "deployment": d})
	})
	mux.HandleFunc("POST /api/v1/apps/{id}/upload", func(w http.ResponseWriter, r *http.Request) {
		body := f.readZip(r)
		j(w, f.build(r.PathValue("id"), body))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeInstall) readZip(r *http.Request) []byte {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		f.t.Errorf("%s: a push that was not a multipart form: %v", f.name, err)
		return nil
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		f.t.Errorf("%s: a push with no file: %v", f.name, err)
		return nil
	}
	defer file.Close()
	b, _ := io.ReadAll(file)
	f.mu.Lock()
	f.received = append(f.received, b)
	f.mu.Unlock()
	return b
}

// build records a deployment that has already succeeded and is now serving,
// which is all the console ever watches for.
func (f *fakeInstall) build(appID string, zip []byte) *Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	d := &Deployment{ID: "dep-" + randHex(4), AppID: appID, Status: "success", Current: true,
		CommitSHA: randHex(20), StartedAt: now, FinishedAt: &now}
	for _, old := range f.deploys[appID] {
		old.Current = false
	}
	f.deploys[appID] = append([]*Deployment{d}, f.deploys[appID]...)
	f.archives[d.ID] = zip
	if f.nextSBOM != "" {
		f.sboms[d.ID] = f.nextSBOM
	}
	return d
}

func (f *fakeInstall) deployment(appID, id string) *Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deploys[appID] {
		if d.ID == id {
			c := *d
			return &c
		}
	}
	return nil
}

// release adds an app (once) and a new serving release of it with these bytes.
func (f *fakeInstall) release(appID, slug, ownerID string, zip []byte) *Deployment {
	f.mu.Lock()
	if f.apps[appID] == nil {
		f.apps[appID] = &App{ID: appID, Slug: slug, Name: strings.ToUpper(slug[:1]) + slug[1:], OwnerID: ownerID}
	}
	f.mu.Unlock()
	return f.build(appID, zip)
}

// --- The install the console runs on -----------------------------------------

type harness struct {
	t      *testing.T
	c      *Console
	h      http.Handler
	source *fakeInstall
	stage  *fakeInstall
	prod   *fakeInstall

	sourceRow, stageRow, prodRow *Target
	users                        map[string]Viewer
	mailMu                       sync.Mutex
	mails                        []string
}

const (
	hAppID   = "mc-app"
	hRoleKey = "test-role-key"
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := testStore(t)
	hs := &harness{t: t, users: map[string]Viewer{
		"ada":   {ID: "ada", Name: "Ada", Email: "ada@example.com"},
		"grace": {ID: "grace", Name: "Grace", Email: "grace@example.com"},
		"linus": {ID: "linus", Name: "Linus", Email: "linus@example.com"},
	}}
	home := http.NewServeMux()
	home.HandleFunc("GET /api/v1/app/viewer", func(w http.ResponseWriter, r *http.Request) {
		v, ok := hs.users[r.Header.Get("X-Toft-Act-For")]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	})
	home.HandleFunc("GET /api/v1/app/self", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"capabilities":["email"]}`)
	})
	home.HandleFunc("POST /api/v1/app/notify/email", func(w http.ResponseWriter, r *http.Request) {
		var m struct{ Subject, Text string }
		_ = json.NewDecoder(r.Body).Decode(&m)
		hs.mailMu.Lock()
		hs.mails = append(hs.mails, m.Subject+"\n"+m.Text)
		hs.mailMu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	home.HandleFunc("POST /api/v1/app/jobs/{name}/runs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{}`)
	})
	homeSrv := httptest.NewServer(home)
	t.Cleanup(homeSrv.Close)

	seal, err := newSealer("test-target-key-secret")
	if err != nil {
		t.Fatal(err)
	}
	u := newURLs("")
	c := &Console{
		cfg:   Config{APIURL: homeSrv.URL, AppToken: "app-token", AppID: hAppID, RoleKey: hRoleKey},
		store: store, seal: seal, urls: u, http: &http.Client{Timeout: 10 * time.Second},
		render:  newRenderer(u),
		viewers: newTTLCache[*Viewer](viewerTTL),
		caps:    newTTLCache[map[string]bool](appWideTTL),
		ticks:   newTTLCache[*tickRun](viewerTTL),
	}
	hs.c, hs.h = c, c.routes()
	hs.source = newFakeInstall(t, "dev")
	hs.stage = newFakeInstall(t, "stage")
	hs.prod = newFakeInstall(t, "prod")
	ctx := context.Background()
	hs.sourceRow = hs.saveTarget(ctx, &Target{ID: "t-dev", Kind: TargetKindSource, Label: "Dev", BaseURL: hs.source.srv.URL})
	hs.stageRow = hs.saveTarget(ctx, &Target{ID: "t-stage", Kind: TargetKindTarget, Label: "Stage", BaseURL: hs.stage.srv.URL})
	hs.prodRow = hs.saveTarget(ctx, &Target{ID: "t-prod", Kind: TargetKindTarget, Label: "Prod", BaseURL: hs.prod.srv.URL,
		RequiresTargetID: "t-stage"})
	if err := store.SetSetting(ctx, SettingAdminGroupEmail, "platform-owners@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReviewer(ctx, Reviewer{ID: "rv1", SubjectType: "user", SubjectID: "grace", SubjectName: "Grace"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReviewer(ctx, Reviewer{ID: "rv2", SubjectType: "user", SubjectID: "linus", SubjectName: "Linus"}); err != nil {
		t.Fatal(err)
	}
	return hs
}

func (hs *harness) saveTarget(ctx context.Context, t *Target) *Target {
	enc, err := hs.c.seal.seal("toftd_" + t.ID)
	if err != nil {
		hs.t.Fatal(err)
	}
	t.DeployKeyEnc, t.KeyPrefix, t.CreatedAt = enc, "toftd_", time.Now().UTC()
	if err := hs.c.store.SaveTarget(ctx, t); err != nil {
		hs.t.Fatal(err)
	}
	return t
}

// roleBlob is X-Toft-Role as the platform mints it.
func roleBlob(role string) string {
	payload := fmt.Sprintf("v1.%s.%s.%d", role, base64.RawURLEncoding.EncodeToString([]byte(hAppID)),
		time.Now().Add(time.Hour).Unix())
	return payload + "." + roleSignature(hRoleKey, payload)
}

// do sends one request to the console as a person. A form is posted the way a
// browser posts it.
func (hs *harness) do(who, role, method, path string, form url.Values) *httptest.ResponseRecorder {
	hs.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("X-Toft-Role", roleBlob(role))
	r.Header.Set("X-Toft-Act-For", who)
	w := httptest.NewRecorder()
	hs.h.ServeHTTP(w, r)
	return w
}

// tick is one pass of the job, as the platform's scheduler would start it.
func (hs *harness) tick() {
	hs.t.Helper()
	hs.c.runTick(context.Background(), "the test")
	hs.c.sends.Wait()
}

// request reads a request back, failing the test if it is gone.
func (hs *harness) request(id string) *Request {
	hs.t.Helper()
	r, err := hs.c.store.GetRequest(context.Background(), id)
	if err != nil || r == nil {
		hs.t.Fatalf("request %s: %v", id, err)
	}
	return r
}

// newest is the most recently raised request.
func (hs *harness) newest() *Request {
	hs.t.Helper()
	all, err := hs.c.store.ListRequests(context.Background())
	if err != nil || len(all) == 0 {
		hs.t.Fatalf("no requests: %v", err)
	}
	return &all[0]
}

// shipToStage raises, approves and runs a request for the source app's
// serving release to Stage, ending deployed. It returns the request.
func (hs *harness) shipToStage(appID string) *Request {
	hs.t.Helper()
	deps := hs.source.deploys[appID]
	w := hs.do("ada", RoleViewer, "POST", "/requests/new", url.Values{
		"app_id": {appID}, "deployment_id": {deps[0].ID}, "target_id": {"t-stage"}, "reason": {"to stage"}})
	if w.Code != http.StatusSeeOther {
		hs.t.Fatalf("raising a Stage request: %d %s", w.Code, w.Body.String())
	}
	req := hs.newest()
	hs.approve(req.ID, "grace", url.Values{"owner_group_id": {"g-stage"}})
	hs.tick() // pushes
	hs.tick() // sees the build
	got := hs.request(req.ID)
	if got.Status != StatusDeployed {
		hs.t.Fatalf("the Stage request ended %s: %s", got.Status, got.Failure)
	}
	return got
}

func (hs *harness) approve(id, who string, form url.Values) {
	hs.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	w := hs.do(who, RoleViewer, "POST", "/requests/"+id+"/approve", form)
	if w.Code != http.StatusSeeOther {
		hs.t.Fatalf("approving %s: %d %s", id, w.Code, refusal(w))
	}
}

// promote raises a promotion of upstream to Prod as Ada and returns it.
func (hs *harness) promote(upstreamID string, extra url.Values) *httptest.ResponseRecorder {
	form := url.Values{"promoted_from": {upstreamID}, "target_id": {"t-prod"}, "reason": {"to prod"}}
	for k, v := range extra {
		form[k] = v
	}
	return hs.do("ada", RoleViewer, "POST", "/requests/new", form)
}

// refusal is the sentence a refused page carries, for a failure message.
func refusal(w *httptest.ResponseRecorder) string {
	b := w.Body.String()
	if i := strings.Index(b, `class="notice`); i >= 0 {
		b = b[i:]
		if len(b) > 600 {
			b = b[:600]
		}
	}
	return strconv.Itoa(w.Code) + " " + b
}
