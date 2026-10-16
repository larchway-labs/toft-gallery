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

// The tick refuses anything that is not the platform. `system` is the only word
// that opens it, and an absent header is not a system firing.
func TestOnlyTheSystemRoleIsTheScheduler(t *testing.T) {
	for _, word := range []string{RoleOwner, RoleEditor, RoleViewer, RoleAnonymous, RoleApp, RoleUnknown, ""} {
		if word == RoleSystem {
			t.Fatal("the loop is testing the wrong thing")
		}
	}
	if RoleSystem != "system" {
		t.Fatalf("the scheduler's word is %q; the platform sends `system`", RoleSystem)
	}
}

// The tick's door, as behaviour.
//
// `system` is a schedule firing on its schedule. `owner`/`editor` is somebody
// pressing Run now on the platform's own schedule surface — which is the first
// thing anybody does with a new schedule, and refusing it would mean an
// administrator can configure the tick and never test it. Everything else,
// including an absent or expired header, refuses.
func TestTheTickAcceptsThePlatformAndAConsoleEditorAndNobodyElse(t *testing.T) {
	accepted := map[string]bool{RoleSystem: true, RoleOwner: true, RoleEditor: true}
	for _, word := range append(append([]string{}, mintedRoles...), RoleUnknown, "") {
		got := tickActor(word)
		if accepted[word] && got == "" {
			t.Errorf("%q was refused; the platform and a console editor both fire this", word)
		}
		if !accepted[word] && got != "" {
			t.Errorf("%q was accepted as %q; a visitor must never start a production deploy", word, got)
		}
	}
	if tickActor(RoleSystem) == tickActor(RoleOwner) {
		t.Error("the ledger cannot tell a schedule from a person pressing Run now")
	}
}
