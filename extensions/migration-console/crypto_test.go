package main

import "testing"

func TestTargetKeysRoundTripAndFailByName(t *testing.T) {
	s, err := newSealer("a passphrase somebody pasted")
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.seal("toftd_secretsecretsecret")
	if err != nil {
		t.Fatal(err)
	}
	if record == "toftd_secretsecretsecret" {
		t.Fatal("the key was stored in the clear")
	}
	back, err := s.open(record)
	if err != nil || back != "toftd_secretsecretsecret" {
		t.Fatalf("round trip: %q, %v", back, err)
	}

	// The overwhelmingly likely cause of a failure here is that
	// TARGET_KEY_SECRET was changed, and the message has to say so: an
	// administrator who reads it fixes this in a minute, where "push failed"
	// costs an afternoon.
	other, _ := newSealer("a different passphrase")
	if _, err := other.open(record); err == nil {
		t.Fatal("a record opened under the wrong secret")
	} else if !contains(err.Error(), "TARGET_KEY_SECRET") {
		t.Errorf("the refusal does not name the cause: %v", err)
	}

	// Every ciphertext names the version that wrote it, so a second scheme can
	// be added without a reader having to guess.
	if record[:3] != "v1." {
		t.Errorf("record %q does not carry its version", record[:8])
	}
	if _, err := s.open("v2.aaaa.bbbb"); err == nil {
		t.Error("a record from an unknown version was opened")
	}
}

// Two seals of the same secret differ: a random nonce per record, so a database
// dump does not reveal which targets share a key.
func TestSealsAreNotDeterministic(t *testing.T) {
	s, _ := newSealer("secret")
	a, _ := s.seal("toftd_same")
	b, _ := s.seal("toftd_same")
	if a == b {
		t.Fatal("two seals of the same key are identical")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
