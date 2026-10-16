// Command volume-browser is a file manager for the EFS volumes attached to a
// Toft app.
//
// It reads TOFT_STORAGE — the platform's own declaration of what is
// mounted — and offers the ordinary operations on each volume it finds:
// browse, download, upload, create a folder, rename, delete. Nothing is
// configured by hand: the mounts are discovered, and a mount that is declared
// but not present is shown as a problem rather than hidden.
//
// The Toft contract this app honors:
//
//	PORT              the port to listen on
//	HOST              the address to bind (127.0.0.1 in shared mode — never 0.0.0.0)
//	BASE_PATH         the prefix the app is served under; the proxy strips it
//	                  before forwarding, so routes are registered at "/"
//	TOFT_STORAGE the attached storage, as JSON — the only source of truth
//	                  about what is mounted where
//
// Isolated mode only, and not by this app's choice: durable storage is attached
// below a workload's own boundary, which a shared process does not have. In
// shared mode TOFT_STORAGE is absent and the app says so instead of
// starting empty and looking broken.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type config struct {
	Host      string
	Port      string
	BasePath  string
	Title     string
	MaxUpload int64 // bytes, per file
	ReadOnly  bool
}

func loadConfig() config {
	cfg := config{
		Host:      envOr("HOST", "127.0.0.1"),
		Port:      envOr("PORT", "8080"),
		BasePath:  os.Getenv("BASE_PATH"),
		Title:     envOr("BROWSER_TITLE", "Volume browser"),
		MaxUpload: 1024 << 20,
		ReadOnly:  truthy(os.Getenv("READ_ONLY")),
	}
	if raw := os.Getenv("MAX_UPLOAD_MB"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case err != nil || n < 1:
			log.Printf("MAX_UPLOAD_MB=%q is not a positive number of megabytes; using %s", raw, humanBytes(cfg.MaxUpload))
		default:
			cfg.MaxUpload = n << 20
		}
	}
	return cfg
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func main() {
	cfg := loadConfig()

	mounts, err := discover(os.Getenv(envStorage))
	if err != nil {
		// A contract this app cannot read is a boot failure, not an empty page.
		log.Fatalf("%v", err)
	}
	if cfg.ReadOnly {
		mounts.forceReadOnly()
	}

	for _, v := range mounts.all() {
		switch {
		case v.Browsable:
			log.Printf("volume %q at %s (%s)", v.Name, v.Path, v.Access)
		default:
			log.Printf("volume %q unusable: %s", v.Name, v.Problem)
		}
	}
	if len(mounts.all()) == 0 {
		log.Printf("no storage is attached: %s is empty or absent. Attach a volume to this app "+
			"and redeploy — a mapping change takes effect on the next deployment.", envStorage)
	}

	srv := &server{cfg: cfg, mounts: mounts}
	addr := cfg.Host + ":" + cfg.Port
	log.Printf("volume browser listening on %s (base path %q, %d browsable of %d mounts)",
		addr, cfg.BasePath, mounts.browsableCount(), len(mounts.all()))

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           logging(srv.routes()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server stopped: %v", err)
	}
}

type server struct {
	cfg    config
	mounts *mountSet
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/api/volumes", s.handleVolumes)
	mux.HandleFunc("/api/list", s.handleList)
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/mkdir", s.handleMkdir)
	mux.HandleFunc("/api/rename", s.handleRename)
	mux.HandleFunc("/api/delete", s.handleDelete)
	return mux
}

// --- responses ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail turns an operation's error into a status and a sentence.
//
// The sentence is the error's own text, because every error this app raises is
// already written for the person reading it. The status is the caller's cue:
// 403 is a mount that will never accept the write, 404 is a path that is not
// there, 400 is a request to fix.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errReadOnly):
		status = http.StatusForbidden
	case errors.Is(err, errNotFound), os.IsNotExist(err):
		status = http.StatusNotFound
	case errors.Is(err, errOutside):
		status = http.StatusForbidden
	case os.IsPermission(err):
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// volumeFor resolves the ?vol= or JSON "vol" every request carries.
func (s *server) volumeFor(name string) (volume, error) {
	v, ok := s.mounts.get(name)
	if !ok {
		return volume{}, fmt.Errorf("no volume named %q is attached to this app", name)
	}
	return v, nil
}

// --- handlers ---

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.mounts.refresh()
	renderPage(w, s.cfg, s.mounts.all())
}

func (s *server) handleVolumes(w http.ResponseWriter, r *http.Request) {
	s.mounts.refresh()
	writeJSON(w, http.StatusOK, map[string]any{
		"volumes":         s.mounts.all(),
		"read_only":       s.cfg.ReadOnly,
		"max_upload":      s.cfg.MaxUpload,
		"max_upload_text": humanBytes(s.cfg.MaxUpload),
	})
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	v, err := s.volumeFor(r.URL.Query().Get("vol"))
	if err != nil {
		fail(w, err)
		return
	}
	rel := cleanRel(r.URL.Query().Get("path"))
	entries, err := list(v, rel)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"volume":   v.Name,
		"path":     rel,
		"writable": v.Writable(),
		"entries":  entries,
	})
}

// handleFile serves one file, as a download by default.
//
// The filename on the wire is the file's own, taken from the resolved path and
// never from a caller-supplied string — the same reason the platform builds a
// download's disposition server-side.
func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	v, err := s.volumeFor(r.URL.Query().Get("vol"))
	if err != nil {
		fail(w, err)
		return
	}
	rel := cleanRel(r.URL.Query().Get("path"))
	f, info, err := openFile(v, rel)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()

	name := path.Base(rel)
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	// Whatever is on an EFS volume was put there by something other than this
	// app, so it is bytes until proven otherwise: no sniffing, and inline
	// preview is served under a sandbox the browser applies to the response.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// handleUpload takes a streamed multipart body.
//
// It uses the reader rather than ParseMultipartForm: the form parser spools
// every part to a temporary file first, which on a pod with a 1Gi scratch
// emptyDir is a smaller limit than the volume it is writing to.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, errors.New("upload is a POST"))
		return
	}
	v, err := s.volumeFor(r.URL.Query().Get("vol"))
	if err != nil {
		fail(w, err)
		return
	}
	if !v.Writable() {
		fail(w, errReadOnly)
		return
	}
	dir := cleanRel(r.URL.Query().Get("path"))
	overwrite := r.URL.Query().Get("overwrite") == "1"

	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, errors.New("that was not a file upload: "+err.Error()))
		return
	}

	type saved struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		Error string `json:"error,omitempty"`
	}
	var results []saved
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if part.FormName() != "file" || name == "" || name == "." {
			_ = part.Close()
			continue
		}
		n, err := save(v, dir, name, part, s.cfg.MaxUpload, overwrite)
		_ = part.Close()
		if err != nil {
			results = append(results, saved{Name: name, Error: err.Error()})
			continue
		}
		results = append(results, saved{Name: name, Size: n})
	}
	if len(results) == 0 {
		fail(w, errors.New("no files were in that upload"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": results})
}

func (s *server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct{ Vol, Path, Name string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	v, err := s.volumeFor(req.Vol)
	if err != nil {
		fail(w, err)
		return
	}
	if err := mkdir(v, req.Path, strings.TrimSpace(req.Name)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"created": req.Name})
}

func (s *server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct{ Vol, Path, From, Name string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	v, err := s.volumeFor(req.Vol)
	if err != nil {
		fail(w, err)
		return
	}
	if err := rename(v, req.From, req.Path, strings.TrimSpace(req.Name)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"renamed": req.Name})
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Vol, Path string
		Recursive bool
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	v, err := s.volumeFor(req.Vol)
	if err != nil {
		fail(w, err)
		return
	}
	if err := remove(v, req.Path, req.Recursive); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": req.Path})
}

// decode reads a small JSON body. The cap is generous for four short strings
// and closes the door on a body that never ends.
func decode(r *http.Request, into any) error {
	if r.Method != http.MethodPost {
		return errors.New("that operation is a POST")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	if err := dec.Decode(into); err != nil {
		return errors.New("that request was not valid JSON: " + err.Error())
	}
	return nil
}

// --- logging ---

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d in %s", r.Method, r.URL.RequestURI(), rec.status, time.Since(start).Round(time.Millisecond))
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
