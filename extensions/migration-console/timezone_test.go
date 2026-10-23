package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The zone on Settings is the clock every time is shown in, and the clock a
// typed schedule is read on.
func TestTheChosenZoneIsHonoredEverywhere(t *testing.T) {
	t.Cleanup(func() { setDisplayZone("") })
	at := time.Date(2026, 10, 23, 19, 58, 0, 0, time.UTC)

	setDisplayZone("America/New_York")
	r := newRenderer(newURLs("/apps/mc"))
	when := r.funcs()["when"].(func(time.Time) string)
	if got := when(at); got != "23 Oct 2026, 15:58 EDT" {
		t.Errorf("when = %q", got)
	}
	d := Deployment{CommitSHA: "abcdef123", StartedAt: at}
	if !strings.Contains(d.Label(), "23 Oct 2026 15:58 EDT") {
		t.Errorf("release label %q is not in the chosen zone", d.Label())
	}

	// The schedule field: 15:58 typed means 15:58 in New York, whatever the
	// browser's offset said.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("tz_offset=60"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()
	got, _ := time.ParseInLocation("2006-01-02T15:04", "2026-10-23T15:58", scheduleZone(req))
	if !got.Equal(at) {
		t.Errorf("15:58 typed read as %s, want %s", got.UTC(), at)
	}

	setDisplayZone("")
	if zoneSet() || zone() != time.Local {
		t.Error("clearing the setting did not return to the install's clock")
	}
	setDisplayZone("Not/AZone")
	if zoneSet() {
		t.Error("an unknown zone name was applied")
	}
	if _, err := loadZone("Local"); err == nil {
		t.Error("\"Local\" was accepted as a zone name")
	}
}
