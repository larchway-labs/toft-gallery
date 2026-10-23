package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// One viewer lookup per session per viewerTTL, not one per page: the console
// shares one rate-limited token among everybody using it.
func TestAViewerIsAskedForOncePerSessionWhileFresh(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Ada"}`))
	}))
	defer srv.Close()
	c := &Console{cfg: Config{AppToken: "tofta_x", APIURL: srv.URL}, http: srv.Client(),
		viewers: newTTLCache[*Viewer](viewerTTL)}

	for _, blob := range []string{"blob-a", "blob-a", "blob-a", "blob-b"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Toft-Act-For", blob)
		if v, err := c.fetchViewer(r.Context(), r); err != nil || v.ID != "u1" {
			t.Fatalf("viewer = %+v, %v", v, err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("asked the platform %d times for two sessions, want 2", n)
	}
}

// A refused lookup is not "nobody": the page says Toft is busy rather than
// telling a person they have no requests and their own request is not theirs.
func TestARateLimitedLookupIsNotAnAnonymousCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited","code":"rate_limited"}`))
	}))
	defer srv.Close()
	c := &Console{cfg: Config{AppToken: "tofta_x", APIURL: srv.URL}, http: srv.Client(),
		viewers: newTTLCache[*Viewer](viewerTTL)}

	c.cfg.RoleKey, c.cfg.AppID = "role-key", "app-1"
	payload := "v1.viewer." + base64.RawURLEncoding.EncodeToString([]byte("app-1")) + "." +
		strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Toft-Role", payload+"."+roleSignature("role-key", payload))
	r.Header.Set("X-Toft-Act-For", "blob")

	caller := c.resolveCaller(r)
	if !caller.Submitter || caller.Viewer != nil || !caller.LookupFailed || caller.IdentityOff {
		t.Fatalf("caller = %+v, want a submitter whose lookup failed", caller)
	}
	if _, ok := c.viewers.get(blobKey("blob"), time.Now()); ok {
		t.Error("a refused lookup was cached")
	}
}

func TestTheCacheForgetsAfterItsTTL(t *testing.T) {
	c := newTTLCache[int](time.Minute)
	now := time.Now()
	c.put("k", 1, now)
	if v, ok := c.get("k", now.Add(59*time.Second)); !ok || v != 1 {
		t.Error("forgot too early")
	}
	if _, ok := c.get("k", now.Add(61*time.Second)); ok {
		t.Error("remembered past its TTL")
	}
}
