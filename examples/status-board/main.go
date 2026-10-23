// Command status-board serves an internal service status page.
//
// It polls a list of HTTP endpoints on an interval and shows the result as a
// board: reachable, slow, or down, with the last error text when there is one.
// Nothing is persisted — the board is what this process has seen since it
// started, which is deliberate: a status page that has to be backed up is a
// service of its own.
//
// The Toft contract this app honors:
//
//	PORT       the port to listen on
//	HOST       the address to bind (127.0.0.1 in shared mode — never 0.0.0.0)
//	BASE_PATH  the prefix the app is served under; the proxy strips it before
//	           forwarding, so routes are registered at "/" and BASE_PATH is
//	           only ever used when emitting an absolute URL.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	cfg := loadConfig()

	board := newBoard(cfg.Services)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go board.run(ctx, cfg.Interval, cfg.Timeout)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderBoard(w, cfg, board.snapshot())
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"checked_every_seconds": int(cfg.Interval.Seconds()),
			"services":              board.snapshot(),
		})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	addr := cfg.Host + ":" + cfg.Port
	log.Printf("status board listening on %s (base path %q, %d services)", addr, cfg.BasePath, len(cfg.Services))

	srv := &http.Server{
		Addr:              addr,
		Handler:           logging(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server stopped: %v", err)
	}
}

// --- configuration ---

type config struct {
	Host     string
	Port     string
	BasePath string
	Title    string
	Interval time.Duration
	Timeout  time.Duration
	Services []service
}

type service struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// loadConfig reads the environment. Every value has a working default, so the
// app starts and explains itself even when nobody filled anything in — an
// app whose first screen is a stack trace teaches nothing.
func loadConfig() config {
	cfg := config{
		Host:     env("HOST", "127.0.0.1"),
		Port:     env("PORT", "8080"),
		BasePath: os.Getenv("BASE_PATH"),
		Title:    env("BOARD_TITLE", "Service status"),
		Interval: time.Duration(envInt("CHECK_INTERVAL_SECONDS", 60)) * time.Second,
		Timeout:  time.Duration(envInt("CHECK_TIMEOUT_SECONDS", 5)) * time.Second,
	}
	cfg.Services = parseServices(os.Getenv("SERVICES"))
	if cfg.Interval < 5*time.Second {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Timeout < time.Second {
		cfg.Timeout = time.Second
	}
	return cfg
}

// parseServices reads the SERVICES variable: comma-separated name=url pairs.
// A malformed entry is skipped with a log line rather than failing the boot —
// one typo should cost one row, not the whole board.
func parseServices(raw string) []service {
	var out []service
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, url, ok := strings.Cut(entry, "=")
		if !ok {
			log.Printf("SERVICES: skipping %q — expected name=url", entry)
			continue
		}
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if name == "" || !strings.HasPrefix(url, "http") {
			log.Printf("SERVICES: skipping %q — needs a name and an http(s) url", entry)
			continue
		}
		out = append(out, service{Name: name, URL: url})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// --- the board ---

// Result is one service's last check. CheckedAt is zero until the first sweep
// finishes, which the page renders as "not checked yet" rather than as down:
// an app that has just started has not observed a failure, and saying it has
// is the fastest way to make a status page nobody trusts.
type Result struct {
	Service    service       `json:"service"`
	Status     string        `json:"status"`
	StatusCode int           `json:"status_code,omitempty"`
	Latency    time.Duration `json:"-"`
	LatencyMS  int64         `json:"latency_ms"`
	Error      string        `json:"error,omitempty"`
	CheckedAt  time.Time     `json:"checked_at"`
}

const (
	statusPending = "pending"
	statusUp      = "up"
	statusSlow    = "slow"
	statusDown    = "down"
)

type board struct {
	mu      sync.RWMutex
	results []Result
	client  *http.Client
}

func newBoard(services []service) *board {
	b := &board{results: make([]Result, len(services))}
	for i, s := range services {
		b.results[i] = Result{Service: s, Status: statusPending}
	}
	return b
}

func (b *board) run(ctx context.Context, every, timeout time.Duration) {
	b.client = &http.Client{Timeout: timeout}
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	b.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sweep(ctx)
		}
	}
}

func (b *board) sweep(ctx context.Context) {
	b.mu.RLock()
	targets := make([]service, len(b.results))
	for i, r := range b.results {
		targets[i] = r.Service
	}
	b.mu.RUnlock()

	var wg sync.WaitGroup
	fresh := make([]Result, len(targets))
	for i, s := range targets {
		wg.Add(1)
		go func(i int, s service) {
			defer wg.Done()
			fresh[i] = b.check(ctx, s)
		}(i, s)
	}
	wg.Wait()

	b.mu.Lock()
	b.results = fresh
	b.mu.Unlock()
}

func (b *board) check(ctx context.Context, s service) Result {
	res := Result{Service: s, CheckedAt: time.Now().UTC()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		res.Status, res.Error = statusDown, err.Error()
		return res
	}
	req.Header.Set("User-Agent", "toft-status-board/1.0")

	start := time.Now()
	resp, err := b.client.Do(req)
	res.Latency = time.Since(start)
	res.LatencyMS = res.Latency.Milliseconds()
	if err != nil {
		res.Status, res.Error = statusDown, trimError(err.Error())
		return res
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	switch {
	case resp.StatusCode >= 500:
		res.Status = statusDown
		res.Error = resp.Status
	case resp.StatusCode >= 400:
		res.Status = statusSlow
		res.Error = resp.Status
	case res.Latency > 2*time.Second:
		res.Status = statusSlow
	default:
		res.Status = statusUp
	}
	return res
}

func (b *board) snapshot() []Result {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Result, len(b.results))
	copy(out, b.results)
	return out
}

// trimError keeps the useful half of a transport error. Go's messages carry the
// whole request context, which reads as noise on a wall board.
func trimError(msg string) string {
	if i := strings.LastIndex(msg, ": "); i > 0 && len(msg) > 80 {
		return msg[i+2:]
	}
	return msg
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
