package main

import "testing"

// Rejection is terminal, and so is everything else that has finished. A
// rejected request is not reopened: reopening would let the serving release
// move under a decision somebody already made, which is the exact thing pinning
// a deployment prevents.
func TestTerminalStatesCannotMove(t *testing.T) {
	for _, s := range []string{StatusDeployed, StatusRejected, StatusWithdrawn, StatusCancelled, StatusFailed} {
		if !Terminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []string{StatusDraft, StatusSubmitted, StatusApproved, StatusDeploying} {
		if Terminal(s) {
			t.Errorf("%s should still be able to move", s)
		}
	}
}

// Drift is attention and never danger: it is not a failure, it is a fact the
// approver needs. Reaching for red to mean "notice this" is how a warning
// colour stops working.
func TestTonesCarryTheRightMeaning(t *testing.T) {
	if tone("drift") != "attention" {
		t.Error("drift is not a failure")
	}
	if tone(LinkDiverged) != "attention" {
		t.Error("diverged is a fact to act on, not a failure")
	}
	if tone(StatusFailed) != "danger" || tone(StatusRejected) != "danger" {
		t.Error("a failure and a rejection are danger")
	}
	if tone(StatusWithdrawn) != "neutral" {
		t.Error("withdrawn is a recorded fact with no valence")
	}
	// Cancelled is neutral for the same reason withdrawn is, and deliberately
	// not danger: nothing broke and nobody was refused. Somebody approved a
	// change and then called it off, which is a change board working.
	if tone(StatusCancelled) != "neutral" {
		t.Error("cancelled is a recorded fact with no valence — nothing failed")
	}
	if tone(LinkNotChecked) != "neutral" {
		t.Error("not checked is neutral — it is not agreement and it is not a failure")
	}
	if tone(RoleSystem) != "info" {
		t.Error("the platform acting is info")
	}
}

// Every status carries its word beside its colour, because a colour is never
// the only carrier.
func TestEveryStateHasAWord(t *testing.T) {
	for _, s := range []string{
		StatusDraft, StatusSubmitted, StatusApproved, StatusDeploying, StatusDeployed,
		StatusRejected, StatusWithdrawn, StatusCancelled, StatusFailed,
		LinkUpToDate, LinkBehind, LinkDiverged, LinkNotChecked,
	} {
		if statusLabel(s) == "" || statusLabel(s) == "—" {
			t.Errorf("%s has no word", s)
		}
	}
}


// Cancelled, withdrawn and rejected are three different endings and the words
// have to stay three different words.
//
// They are the ones a reader is most likely to collapse — all three mean "it
// did not go" — and the distinction is exactly what a change board is asked a
// year later: was it stopped before anybody looked at it (withdrawn), refused
// by a reviewer (rejected), or approved and then called off (cancelled)? A
// product that renders any two of those the same has lost the answer.
func TestTheThreeWaysItDoesNotGoAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{StatusWithdrawn, StatusRejected, StatusCancelled} {
		w := statusLabel(s)
		if prev, dup := seen[w]; dup {
			t.Errorf("%s and %s both read as %q", prev, s, w)
		}
		seen[w] = s
	}
	if StatusCancelled == StatusWithdrawn {
		t.Error("cancelling after an approval is not withdrawing before a decision")
	}
}
