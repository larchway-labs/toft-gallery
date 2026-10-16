package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Config is everything the app learns from its environment.
//
// Toft injects all of it. Nothing here is a setting somebody types into
// the app's own Settings tab, which is the point: an app should not have to be
// told where it is mounted or who it is.
type Config struct {
	Port     string // PORT — the platform picks it; always bind 127.0.0.1
	BasePath string // BASE_PATH — the mount prefix, stripped before we see it
	DataDir  string // where stockroom.db lives (see resolveDataDir)
	DataNote string // one line explaining which of the four candidates won

	AppID    string
	AppSlug  string
	AppName  string
	APIURL   string
	AppToken string

	// Mounts is TOFT_STORAGE, decoded. Empty on a shared-mode app, which
	// is the ordinary case and not a fault.
	Mounts []Mount
}

// Mount is one entry of TOFT_STORAGE. Resolution is by name, never by
// path — an administrator can move a resource without this app being edited.
type Mount struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Access string `json:"access"`
}

func (m Mount) Writable() bool { return m.Access == "readwrite" || m.Access == "rw" }

func loadConfig() Config {
	c := Config{
		Port:     env("PORT", "8080"),
		BasePath: strings.TrimSuffix(env("BASE_PATH", ""), "/"),
		AppID:    os.Getenv("TOFT_APP_ID"),
		AppSlug:  os.Getenv("TOFT_APP_SLUG"),
		AppName:  env("APP_NAME", "Stockroom"),
		APIURL:   strings.TrimSuffix(os.Getenv("TOFT_API_URL"), "/"),
		AppToken: os.Getenv("TOFT_APP_TOKEN"),
	}
	if raw := os.Getenv("TOFT_STORAGE"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Mounts); err != nil {
			log.Printf("stockroom: TOFT_STORAGE is not valid JSON, ignoring it: %v", err)
		}
	}
	c.DataDir, c.DataNote = resolveDataDir(c.Mounts)
	return c
}

// resolveDataDir picks where the SQLite file goes, most durable first.
//
// The order is the whole story of what this app promises about its data. A
// writable storage mount survives redeploys; the scratch directory does not —
// it is wiped on every deploy and restart, and nothing written there is backed
// up. Stockroom is a demonstration, so landing on scratch is fine; what would
// not be fine is landing there without saying so, which is why the reason
// travels with the path and is printed on the Platform tab.
func resolveDataDir(mounts []Mount) (dir, note string) {
	if v := os.Getenv("STOCKROOM_DATA_DIR"); v != "" {
		return v, "STOCKROOM_DATA_DIR is set, so that path wins over everything else."
	}
	for _, m := range mounts {
		if m.Writable() {
			return filepath.Join(m.Path, "stockroom"),
				"Writing to the storage resource \"" + m.Name + "\". This data survives redeploys."
		}
	}
	if v := os.Getenv("TOFT_SCRATCH_DIR"); v != "" {
		return filepath.Join(v, "stockroom"),
			"No writable storage is mapped, so this is the scratch directory. " +
				"It is wiped on every deploy and restart — the register is re-seeded from scratch each time."
	}
	return "data", "Running outside Toft: the database is ./data/stockroom.db."
}

// url prefixes an app-relative path with the mount point.
//
// The proxy strips /apps/{slug} before the request reaches this process, so
// the server routes at root — but every URL the browser will act on has to
// carry the prefix back. Getting this wrong is the single most common way a
// Go app works locally and 404s once deployed.
func (c Config) url(p string) string {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return c.BasePath + p
}

// bare strips the mount prefix, and is the exact inverse of url().
//
// It exists because a redirect is the one URL an app must *not* prefix. The
// platform's proxy prepends BASE_PATH to every Location header it forwards for
// a prefix-stripping framework, so an app that prefixes it too sends the
// browser to /apps/slug/apps/slug/… — a 404 that appears only once deployed and
// never in local development, where BASE_PATH is empty and the bug is invisible.
//
// Redirect targets can arrive either way here: written bare in a handler, or
// round-tripped through a form's "back" field, which carries a prefixed URL
// because it was rendered as a link. Normalising both in one place is what
// keeps that distinction from having to be remembered at eleven call sites.
func (c Config) bare(p string) string {
	if c.BasePath == "" {
		return p
	}
	if p == c.BasePath {
		return "/"
	}
	if strings.HasPrefix(p, c.BasePath+"/") || strings.HasPrefix(p, c.BasePath+"?") {
		return strings.TrimPrefix(p, c.BasePath)
	}
	return p
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
