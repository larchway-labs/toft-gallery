package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The report job against a stand-in for the platform: what it sends, with which
// credential, and that its exit code is the delivery's outcome.
func runJobAgainst(t *testing.T, params string, answer func(path string) int) (int, map[string]string, []string) {
	t.Helper()
	var mu sync.Mutex
	auth := map[string]string{}
	var progress []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth[r.URL.Path] = r.Header.Get("Authorization")
		if r.URL.Path == "/api/v1/runner/progress" {
			body, _ := io.ReadAll(r.Body)
			var p map[string]any
			_ = json.Unmarshal(body, &p)
			progress = append(progress, p["version"].(string)+":"+p["message"].(string))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		code := answer(r.URL.Path)
		w.WriteHeader(code)
		if code < 400 {
			_, _ = io.WriteString(w, `{"send_id":"snd_1","status":"queued"}`)
		} else {
			_, _ = io.WriteString(w, `{"error":"Slack is not attached to this app"}`)
		}
	}))
	defer srv.Close()

	t.Setenv("STOCKROOM_DATA_DIR", t.TempDir())
	t.Setenv("TOFT_API_URL", srv.URL)
	t.Setenv("TOFT_APP_TOKEN", "")
	t.Setenv("TOFT_JOB_TOKEN", "run-token")
	t.Setenv("TOFT_RUN_ID", "0f3c9a21-run")
	t.Setenv("TOFT_TRIGGER", "schedule")
	t.Setenv("TOFT_PARAMS", params)
	code := runReportJob(loadConfig())
	return code, auth, progress
}

func TestReportJobSendsWithTheRunToken(t *testing.T) {
	code, auth, progress := runJobAgainst(t, `{"report":"low_stock","channel":"slack"}`,
		func(string) int { return http.StatusAccepted })
	if code != 0 {
		t.Fatalf("exit %d, want 0 for a delivered report", code)
	}
	if got := auth["/api/v1/app/notify/slack/file"]; got != "Bearer run-token" {
		t.Errorf("the PDF went with %q; a job holds no app token and sends with its run's", got)
	}
	if _, asked := auth["/api/v1/app/self"]; asked {
		t.Error("the job asked /app/self, which refuses a run token; it should not ask")
	}
	if len(progress) == 0 || !strings.HasPrefix(progress[len(progress)-1], "2:") ||
		!strings.Contains(progress[len(progress)-1], "sent") {
		t.Errorf("progress = %v, want the runner contract version and a final summary", progress)
	}
}

func TestReportJobFailsWhenASendFails(t *testing.T) {
	code, _, _ := runJobAgainst(t, `{"report":"inventory","channel":"both","to":"ops@example.com"}`,
		func(path string) int {
			if strings.HasPrefix(path, "/api/v1/app/notify/slack") {
				return http.StatusForbidden
			}
			return http.StatusAccepted
		})
	if code == 0 {
		t.Fatal("exit 0 with Slack refused; partial delivery is a failed run")
	}
}
