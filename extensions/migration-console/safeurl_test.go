package main

import "testing"

// An address somebody typed is server-side input.
//
// The private-range allow-list is the one deliberate exception, and it exists
// because an on-premises estate's other installs are frequently on RFC1918 — a
// blanket refusal would refuse only the intended case. That is this console's
// decision to make and state; it is not the platform's safefetch.
func TestTargetAddressesAreRefusedForTheRightReasons(t *testing.T) {
	none := []string{}

	for _, c := range []struct{ in, why string }{
		{"", "an address is required"},
		{"ftp://example.com", "must start with https"},
		{"https://user:pw@example.com", "credentials"},
		{"https://example.com/api/v1", "no path"},
		{"http://127.0.0.1:8080", "private"},
		{"https://10.0.0.5", "private"},
	} {
		if _, err := validateTargetURL(c.in, none); err == nil {
			t.Errorf("%q was accepted; it should be refused because it %s", c.in, c.why)
		}
	}
}

func TestAnAllowedPrivateRangeIsAccepted(t *testing.T) {
	got, err := validateTargetURL("https://10.0.0.5", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("an allowed private address was refused: %v", err)
	}
	if got != "https://10.0.0.5" {
		t.Errorf("normalised to %q", got)
	}
	// http is accepted inside an allowed range and nowhere else: an estate on
	// RFC1918 frequently terminates TLS somewhere else.
	if _, err := validateTargetURL("http://10.0.0.5", []string{"10.0.0.0/8"}); err != nil {
		t.Errorf("plain http inside an allowed range was refused: %v", err)
	}
	if _, err := validateTargetURL("http://10.0.0.5", []string{"192.168.0.0/16"}); err == nil {
		t.Error("a private address outside every allowed range was accepted")
	}
}

func TestASlugIsWhatTheAppWillAnswerOn(t *testing.T) {
	for in, want := range map[string]string{
		"Orders Viewer":  "orders-viewer",
		"  ORDERS  ":     "orders",
		"a--b":           "a-b",
		"-lead-trail-":   "lead-trail",
		"weird!!chars??": "weird-chars",
	} {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
