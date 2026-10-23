package main

import (
	"strings"
	"testing"
	"time"
)

// The vector is the platform's own output.
//
// It was produced by internal/auth.MintRoleWith in the Toft repository and
// is pinned here, because the *only* way a signing change in the platform
// becomes a test failure in this app — rather than an extension that silently
// stops trusting anything — is a fixed input with a fixed answer on both sides.
const (
	vectorKey   = "test-role-key"
	vectorApp   = "app-123"
	vectorBlob  = "v1.editor.YXBwLTEyMw.4102444800.PUCpIap1dnjndSzldVEXj3SqwKbhEpb2safG6-Fum18"
	vectorAfter = 4102444800
)

func before(t *testing.T) time.Time { t.Helper(); return time.Unix(vectorAfter-60, 0) }

func TestVerifyRoleMatchesThePlatform(t *testing.T) {
	if got := verifyRole(vectorKey, vectorBlob, vectorApp, before(t)); got != RoleEditor {
		t.Fatalf("role = %q, want editor — this app and the platform no longer agree on the signature", got)
	}
}

// Every failure is one word, and that word refuses. Wrong key, wrong app, wrong
// shape, expired: four different failures with one safe answer, because a
// verification that distinguished them would be a verification whose failure
// modes are an oracle.
func TestEveryVerificationFailureIsUnknown(t *testing.T) {
	cases := map[string]struct {
		key, blob, app string
		now            time.Time
	}{
		"wrong key":     {"other-key", vectorBlob, vectorApp, before(t)},
		"another app":   {vectorKey, vectorBlob, "app-999", before(t)},
		"expired":       {vectorKey, vectorBlob, vectorApp, time.Unix(vectorAfter+1, 0)},
		"absent header": {vectorKey, "", vectorApp, before(t)},
		"absent key":    {"", vectorBlob, vectorApp, before(t)},
		"no signature":  {vectorKey, "v1.editor.YXBwLTEyMw.4102444800", vectorApp, before(t)},
		"tampered word": {vectorKey, strings.Replace(vectorBlob, "editor", "owner", 1), vectorApp, before(t)},
		"junk":          {vectorKey, "nonsense", vectorApp, before(t)},
	}
	for name, c := range cases {
		if got := verifyRole(c.key, c.blob, c.app, c.now); got != RoleUnknown {
			t.Errorf("%s resolved to %q; every failure must be unknown, and unknown must refuse", name, got)
		}
	}
}

// A blob signed with this app's own key cannot introduce a seventh value into
// code that switches on six.
func TestOnlyTheSixWordsAreAccepted(t *testing.T) {
	for _, word := range mintedRoles {
		if word == "" {
			t.Fatal("an empty role is in the list")
		}
	}
	if len(mintedRoles) != 6 {
		t.Fatalf("%d role words; the platform mints six and there is no seventh", len(mintedRoles))
	}
}

// The ledger's "fired by" for a tick, from the two words every run carries.
//
// The tick is a job now, so nothing verifies a header to let it in — the
// platform decided who may start it before it ran. What is left is the record:
// "who put release M on production" has to separate the schedule from a
// person, and a person on Toft's Automation tab from one in this console.
func TestTheTickNamesWhoFiredIt(t *testing.T) {
	triggers := []string{"schedule", "ui", "cli", "viewer", "app", ""}
	seen := map[string]string{}
	for _, tr := range triggers {
		got := tickActor(tr, RoleEditor)
		if got == "" {
			t.Errorf("trigger %q names nobody", tr)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("triggers %q and %q both read %q", prev, tr, got)
		}
		seen[got] = tr
	}
	if tickActor("schedule", RoleSystem) != "the platform’s scheduler" {
		t.Errorf("a schedule firing reads %q", tickActor("schedule", RoleSystem))
	}
	if got := tickActor("ui", RoleOwner); !strings.Contains(got, "owner") {
		t.Errorf("a person's run drops their role: %q", got)
	}
	if got := tickActor("ui", RoleSystem); strings.Contains(got, "system") {
		t.Errorf("only a person's role is named, not the platform's word: %q", got)
	}
}
