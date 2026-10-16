package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server holds the three things every handler needs and nothing else.
type Server struct {
	cfg   Config
	store *Store
	plat  *Platform
	tpl   map[string]*template.Template
}

func main() {
	cfg := loadConfig()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("stockroom: ")

	store, err := openStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("the register would not open: %v", err)
	}
	defer store.Close()
	if err := store.SeedIfEmpty(); err != nil {
		log.Fatalf("seeding the register: %v", err)
	}

	srv := &Server{cfg: cfg, store: store, plat: newPlatform(cfg)}
	if err := srv.parseTemplates(); err != nil {
		log.Fatalf("parsing templates: %v", err)
	}

	// Bind loopback, always. An app listening on 0.0.0.0 is reachable without
	// passing through Toft, which means without passing through anybody's
	// authorization — the one mistake in a Go app here that is silently unsafe
	// rather than visibly broken.
	addr := "127.0.0.1:" + cfg.Port
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Generous: a report over a few thousand movements is built inside the
		// request, and a scheduled task that is cut off here records a failure
		// for work that finished.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		log.Printf("listening on http://%s (mounted at %q)", addr, orRoot(cfg.BasePath))
		log.Printf("data: %s — %s", cfg.DataDir, cfg.DataNote)
		if srv.plat.Available() {
			log.Printf("platform API: %s", cfg.APIURL)
		} else {
			log.Printf("no TOFT_API_URL/TOFT_APP_TOKEN — Slack and email are off, " +
				"and every surface that offers them says so")
		}
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serving: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Print("stopping")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /inventory", s.handleInventory)
	mux.HandleFunc("GET /inventory/new", s.handleItemForm)
	mux.HandleFunc("POST /inventory", s.handleItemCreate)
	mux.HandleFunc("GET /inventory/{id}", s.handleItemDetail)
	mux.HandleFunc("POST /inventory/{id}", s.handleItemUpdate)
	mux.HandleFunc("POST /inventory/{id}/delete", s.handleItemDelete)
	mux.HandleFunc("POST /inventory/{id}/move", s.handleQuickMove)

	mux.HandleFunc("GET /movements", s.handleMovements)
	mux.HandleFunc("POST /movements", s.handleMovementCreate)

	mux.HandleFunc("GET /reports", s.handleReports)
	mux.HandleFunc("POST /reports/deliver", s.handleReportDeliver)

	mux.HandleFunc("GET /activity", s.handleActivity)
	mux.HandleFunc("GET /platform", s.handlePlatform)

	// The scheduled task's endpoint. Both verbs, because a task configured as
	// GET and one configured as POST should both reach it — the platform sends
	// whichever the task says, and an app that only answered one would fail with
	// a 405 nobody would think to look for.
	mux.HandleFunc("GET /tasks/report", s.handleScheduledReport)
	mux.HandleFunc("POST /tasks/report", s.handleScheduledReport)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","items":%d}`, s.store.count("items"))
	})

	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(static)))))

	return logRequests(mux)
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Short, not immutable: the assets are embedded in the binary and change
		// only on a deploy, which also changes the process. A long max-age would
		// outlive the release it belongs to.
		w.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		// The cron header is worth a line of its own: a scheduled firing and a
		// person clicking the same button are indistinguishable in a log that
		// prints only the path.
		tag := ""
		if r.Header.Get("X-Toft-Cron") != "" {
			tag = " [scheduled " + r.Header.Get("X-Toft-Schedule") + "]"
		}
		log.Printf("%s %s %d %s%s", r.Method, r.URL.Path, rec.status,
			time.Since(start).Round(time.Millisecond), tag)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// ---------- rendering ----------

// Every page is layout + partials + its own file, parsed once at boot. A
// template that fails to parse takes the process down at start rather than
// producing a 500 on the one page nobody opened during testing.
func (s *Server) parseTemplates() error {
	pages, err := fs.Glob(templateFS, "templates/page-*.html")
	if err != nil {
		return err
	}
	s.tpl = make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(page, "templates/page-"), ".html")
		t, err := template.New("layout.html").Funcs(s.funcs()).
			ParseFS(templateFS, "templates/layout.html", "templates/partials.html", page)
		if err != nil {
			return fmt.Errorf("%s: %w", page, err)
		}
		s.tpl[name] = t
	}
	if len(s.tpl) == 0 {
		return errors.New("no page templates were embedded")
	}
	return nil
}

// page is the envelope every template receives.
type page struct {
	Title  string
	Tab    string
	Cfg    Config
	Flash  *Flash
	Actor  string
	Data   any
	Deploy string    // the app's own name, for the header
	Rail   []RailCap // the capability rail every example in the gallery carries
}

// RailCap is one chip on the rail across the top of every page.
//
// The rail is the gallery's shared device: it names the platform capabilities an
// example is using and says, live, whether each is really there on the install it
// is running on. The point is not that these features exist — it is that an app
// can ask, and can say something useful when the answer is no.
type RailCap struct {
	Label string
	State string // on | warn | off
	Note  string
}

func (s *Server) rail(r *http.Request) []RailCap {
	cap := func(label, state, note string) RailCap {
		return RailCap{Label: label, State: state, Note: note}
	}
	on := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}

	self, _ := s.self(r.Context())
	mode := "unknown"
	if self != nil {
		mode = self.Mode
	}

	durable := false
	for _, m := range s.cfg.Mounts {
		if m.Writable() {
			durable = true
			break
		}
	}
	if os.Getenv("STOCKROOM_DATA_DIR") != "" {
		durable = true
	}

	return []RailCap{
		cap("Go", "on", "Detected from go.mod plus a main package; built with CGO off."),
		cap(mode+" mode", on(self != nil), "GET /api/v1/app/self, cached for a minute."),
		cap("Slack", on(self.Can("slack.post")), capNote(self.Can("slack.post"))),
		cap("Email", on(self.Can("email.send")), capNote(self.Can("email.send"))),
		cap("Scheduled tasks", "on",
			"An HTTP request to a path this app published. It needs nothing switched on."),
		cap("Storage", on(durable), s.cfg.DataNote),
	}
}

func capNote(attached bool) string {
	if attached {
		return "Attached by an administrator on this app's Integrations tab."
	}
	return "Not attached. This app cannot switch it on, and says so rather than failing at 2am."
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name, tab, title string, data any) {
	t, ok := s.tpl[name]
	if !ok {
		http.Error(w, "no such page", http.StatusNotFound)
		return
	}
	p := page{
		Title:  title,
		Tab:    tab,
		Cfg:    s.cfg,
		Flash:  takeFlash(w, r),
		Actor:  actorFrom(r),
		Data:   data,
		Deploy: s.cfg.AppName,
		Rail:   s.rail(r),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, p); err != nil {
		// The response is already partly written by the time this fires, so
		// there is nothing useful to send: log it and let the truncated page
		// speak for itself.
		log.Printf("rendering %s: %v", name, err)
	}
}

func orRoot(s string) string {
	if s == "" {
		return "/"
	}
	return s
}
