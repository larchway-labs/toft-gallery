// Command migration-console is a Toft Gallery extension: the change-board
// workflow for moving an app from the install developers use to the install
// customers use.
//
// A developer opens a request to move one of their apps to another environment,
// attaches whatever the change board wants to see, and submits it. A named
// reviewer reads the request, looks at the attachments, and approves or rejects.
// An approval is followed — immediately, or at a time somebody scheduled — by a
// push that either creates the app on the target or ships a new version of one
// that is already there.
//
// # What it runs as
//
// An ordinary workload with no platform authority of its own. It never acts as
// the person in front of it: it holds no credential of theirs and is offered
// none. Its power is two keys an administrator pasted into it — a read-scoped
// `toftd_` on this install, which can pull a release and cannot deploy anything,
// and a deploy-scoped `toftd_` on each target.
//
// # The Toft contract it honors
//
//	PORT, HOST, BASE_PATH   the listener, and the prefix the proxy strips on
//	                        the way in and re-adds to a Location header
//	TOFT_API_URL       this install's own API
//	TOFT_APP_TOKEN     this app's outbound token (viewer, self, notify)
//	TOFT_APP_ID        for verifying X-Toft-Role
//	TOFT_ROLE_KEY      the per-app, per-start key that header is signed with
//	DATABASE_URL            the operator's Postgres — the ledger, sensitive
//	TARGET_KEY_SECRET       encrypts each target's deploy key at rest, sensitive
//
// and, when the same binary runs as `migration-console tick` — the `tick` job
// in toft.toml — the job's own:
//
//	TOFT_JOB_TOKEN     the run's credential: mail while it is in flight, nothing else
//	TOFT_TRIGGER       schedule, ui, cli, app or viewer — how the run started
//	TOFT_TRIGGER_ROLE  who did: `system` for the schedule, else a role word
//
// # Why Go templates and htmx, and no npm anywhere
//
// A Toft Go deploy runs the SDK preparation and `go build` and nothing
// else. Anything with a Node build step has to be compiled at authoring time
// and committed — an embedded SPA, a dist/ in the repository, a second
// toolchain — which for an app that is almost entirely forms, lists, a detail
// page and a queue is a large amount of machinery bought to render a table.
// htmx is vendored and served from this app's own path: an on-premises install
// may have no egress at all, and an extension that needs a CDN to render its
// own buttons is one that does not work where it is most needed.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Console is the whole app: configuration, the ledger, the platform clients it
// builds per request from the target rows, and the templates.
type Console struct {
	cfg    Config
	store  *Store
	seal   *sealer
	urls   urls
	http   *http.Client
	render *renderer
	// sends counts mail still on its way. The web app never waits on it; the
	// tick job does, because a job that exits takes its goroutines with it.
	sends sync.WaitGroup

	// Answers from the platform's app surface, kept so the console stays
	// inside its token's rate limit (cache.go).
	viewers *ttlCache[*Viewer]
	caps    *ttlCache[map[string]bool]
	ticks   *ttlCache[*tickRun]
}

// newConsole opens the ledger and assembles the app, for either entrypoint.
func newConsole(ctx context.Context, cfg Config) (*Console, func()) {
	store, err := openStore(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	seal, err := newSealer(cfg.TargetKeySecret)
	if err != nil {
		store.Close()
		log.Fatalf("TARGET_KEY_SECRET: %v", err)
	}
	return &Console{
		cfg:   cfg,
		store: store,
		seal:  seal,
		urls:  newURLs(cfg.BasePath),
		// Short, because every call this makes is to the install it is running
		// on. Reaching a *target* goes through Platform, which has its own.
		http:   &http.Client{Timeout: 20 * time.Second},
		render: newRenderer(newURLs(cfg.BasePath)),

		viewers: newTTLCache[*Viewer](viewerTTL),
		caps:    newTTLCache[map[string]bool](appWideTTL),
		ticks:   newTTLCache[*tickRun](viewerTTL),
	}, store.Close
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("migration-console: ")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c, closeStore := newConsole(ctx, cfg)

	// One binary, two entrypoints. `tick` is the job toft.toml declares: one
	// pass over the ledger, then exit, with stdout as the run's log and the
	// exit code as its outcome. Everything else is the web app.
	if len(os.Args) > 1 && os.Args[1] == "tick" {
		code := c.tickMain(ctx)
		closeStore()
		os.Exit(code)
	}
	defer closeStore()

	srv := &http.Server{
		Addr:              cfg.Host + ":" + cfg.Port,
		Handler:           c.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("listening on %s (base path %q)", srv.Addr, cfg.BasePath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
