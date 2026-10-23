package main

import (
	"strings"
	"sync/atomic"
	"time"

	// The zone database, embedded: a container image may have no
	// /usr/share/zoneinfo, and a setting that names "Europe/London" must
	// resolve wherever the console runs.
	_ "time/tzdata"
)

// SettingTimeZone is the console's display zone: an IANA name such as
// "America/New_York", or empty for the install's own clock. Every time the
// console shows — pages, history, release labels, mail — is written in it,
// and a time typed into the schedule field is read in it, so what is typed and
// what is shown are always the same clock.
const SettingTimeZone = "time_zone"

var displayZone atomic.Pointer[time.Location]

// zone is the display zone, or the process's own when none is set.
func zone() *time.Location {
	if z := displayZone.Load(); z != nil {
		return z
	}
	return time.Local
}

// zoneSet reports whether an administrator chose a zone.
func zoneSet() bool { return displayZone.Load() != nil }

// setDisplayZone applies a setting's value. Empty clears it; a name that does
// not resolve is ignored, which only a hand-edited database can produce —
// the settings form refuses one.
func setDisplayZone(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		displayZone.Store(nil)
		return
	}
	if loc, err := loadZone(name); err == nil {
		displayZone.Store(loc)
	}
}

// loadZone resolves an IANA name, refusing the two that LoadLocation accepts
// but nobody means here: "Local" (whatever the container happens to be) and
// an empty string.
func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errBadZone
	}
	return time.LoadLocation(name)
}

var errBadZone = &zoneError{}

type zoneError struct{}

func (*zoneError) Error() string { return "not a time zone name" }

// inZone formats t in the display zone.
func inZone(t time.Time, layout string) string { return t.In(zone()).Format(layout) }

// commonZones is what the settings field suggests. Any IANA name is accepted;
// these are only the ones people most often look for.
var commonZones = []string{
	"UTC",
	"America/New_York", "America/Chicago", "America/Denver", "America/Phoenix",
	"America/Los_Angeles", "America/Anchorage", "Pacific/Honolulu",
	"America/Toronto", "America/Vancouver", "America/Mexico_City", "America/Sao_Paulo",
	"Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin", "Europe/Madrid",
	"Europe/Amsterdam", "Europe/Stockholm", "Europe/Zurich",
	"Africa/Johannesburg", "Asia/Dubai", "Asia/Kolkata", "Asia/Singapore",
	"Asia/Hong_Kong", "Asia/Tokyo", "Asia/Seoul", "Australia/Sydney", "Pacific/Auckland",
}
