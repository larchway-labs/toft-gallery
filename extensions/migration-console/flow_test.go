package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The staged-promotion flow end to end: Dev → Stage → Prod through the
// console's own routes and tick, against fake installs. See harness_test.go.

var orders = []byte("PK\x03\x04 orders release one")

func digestOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// stagedApp is a source app with one release, deployed to Stage through the
// console, with Stage recording a locked npm SBOM for it.
func stagedApp(t *testing.T) (*harness, *Request) {
	hs := newHarness(t)
	hs.source.release("orders", "orders", "ada", orders)
	hs.stage.nextSBOM = sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.0", "npm:b@2.0.0")
	up := hs.shipToStage("orders")
	return hs, up
}

// §7 test 1. **A promoted push never calls Archive.** The source has even
// pruned the release, which is the case the kept bundle exists for; Prod must
// receive the bytes Stage did, digest for digest.
func TestAPromotedPushNeverDownloadsAndShipsStagesBytes(t *testing.T) {
	hs, up := stagedApp(t)
	if up.BundleSHA256 != digestOf(orders) {
		t.Fatalf("Stage's push recorded %s for bytes whose digest is %s", up.BundleSHA256, digestOf(orders))
	}
	kept, _ := hs.c.store.BundleInfo(context.Background(), up.ID)
	if kept == nil || kept.SBOM == "" {
		t.Fatalf("Stage's push kept no bundle, or no SBOM with it: %+v", kept)
	}

	// Dev prunes the release.
	delete(hs.source.archives, up.SourceDeploymentID)
	hits := hs.source.archiveHits

	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("promoting: %s", refusal(w))
	}
	prom := hs.newest()
	if prom.PromotedFrom != up.ID || prom.SourceDeploymentID != up.SourceDeploymentID {
		t.Fatalf("the promotion did not copy its upstream: %+v", prom)
	}
	hs.approve(prom.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})
	hs.tick()
	if hs.source.archiveHits != hits {
		t.Fatalf("the promoted push downloaded from the source (%d archive reads)", hs.source.archiveHits-hits)
	}
	got := hs.request(prom.ID)
	if got.Status != StatusDeploying {
		t.Fatalf("the promotion is %s: %s", got.Status, got.Failure)
	}
	if len(hs.prod.received) != 1 || !bytes.Equal(hs.prod.received[0], orders) {
		t.Fatalf("Prod received something other than Stage's bytes")
	}
	if got.BundleSHA256 != up.BundleSHA256 {
		t.Errorf("Prod's request recorded %s; Stage sent %s", got.BundleSHA256, up.BundleSHA256)
	}
}

// §7 test 3. **Condition 3 is read live at the push.** Approved, then Stage is
// redeployed outside the console: the push fails and names what Stage serves.
func TestThePushRereadsWhatStageServes(t *testing.T) {
	hs, up := stagedApp(t)
	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("promoting: %s", refusal(w))
	}
	prom := hs.newest()
	hs.approve(prom.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})

	other := hs.stage.build(up.TargetAppID, []byte("somebody else's bytes"))
	other.CommitSHA = "cafebabe1234"

	hs.tick()
	got := hs.request(prom.ID)
	if got.Status != StatusFailed {
		t.Fatalf("the promotion is %s after Stage moved under it", got.Status)
	}
	if !strings.Contains(got.Failure, "cafebabe") {
		t.Errorf("the failure does not name what Stage serves now: %q", got.Failure)
	}
	if len(hs.prod.received) != 0 {
		t.Error("bytes reached Prod anyway")
	}
}

// D3. A request naming Prod directly is refused; a promotion ignores a forged
// release and copies it from upstream.
func TestProdTakesOnlyPromotionsAndCopiesTheRelease(t *testing.T) {
	hs, up := stagedApp(t)
	newer := hs.source.release("orders", "orders", "ada", []byte("PK newer, never staged"))

	w := hs.do("ada", RoleViewer, "POST", "/requests/new", url.Values{
		"app_id": {"orders"}, "deployment_id": {newer.ID}, "target_id": {"t-prod"}})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Promote a deployed Stage request") {
		t.Fatalf("a direct request to Prod: %s", refusal(w))
	}

	w = hs.promote(up.ID, url.Values{"deployment_id": {newer.ID}, "app_id": {"something-else"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("promoting: %s", refusal(w))
	}
	prom := hs.newest()
	if prom.SourceDeploymentID != up.SourceDeploymentID || prom.SourceAppID != "orders" {
		t.Errorf("the form's release was used: %s, want %s", prom.SourceDeploymentID, up.SourceDeploymentID)
	}
	checks, _ := hs.c.store.Checks(context.Background(), prom.ID)
	if c := CheckOf(checks, CheckDependencies); c == nil || !strings.HasPrefix(c.Summary, "On Stage") {
		t.Errorf("the dependency verdict was not read from Stage: %+v", c)
	}

	// The requester is not the Prod reviewer.
	if w := hs.do("ada", RoleViewer, "POST", "/requests/"+prom.ID+"/approve", url.Values{}); w.Code != http.StatusForbidden {
		t.Errorf("the requester approved their own promotion: %d", w.Code)
	}
}

// D4 condition 2 at raise: an older Stage request is not promotable once a
// newer one has deployed.
func TestOnlyTheNewestStageReleasePromotes(t *testing.T) {
	hs, first := stagedApp(t)
	hs.source.release("orders", "orders", "ada", []byte("PK release two"))
	hs.shipToStage("orders")
	w := hs.promote(first.ID, nil)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "newer release") {
		t.Fatalf("promoting the older Stage request: %s", refusal(w))
	}
}

// D6. A Stage push whose bundle cannot be kept fails before the hand-off.
func TestAFailedBundleWriteFailsTheStagePushBeforeTheHandOff(t *testing.T) {
	hs := newHarness(t)
	hs.source.release("orders", "orders", "ada", orders)
	if _, err := hs.c.store.pool.Exec(context.Background(),
		`ALTER TABLE mc_bundles ADD CONSTRAINT refuse_all CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	w := hs.do("ada", RoleViewer, "POST", "/requests/new", url.Values{
		"app_id": {"orders"}, "deployment_id": {hs.source.deploys["orders"][0].ID}, "target_id": {"t-stage"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("raising: %s", refusal(w))
	}
	req := hs.newest()
	hs.approve(req.ID, "grace", url.Values{"owner_group_id": {"g-stage"}})
	hs.tick()
	got := hs.request(req.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Failure, "keep the bundle") {
		t.Fatalf("the Stage push is %s: %q", got.Status, got.Failure)
	}
	if len(hs.stage.received) != 0 {
		t.Error("the bytes were handed to Stage although they could not be kept")
	}
}

// A target nothing requires keeps nothing.
func TestAnUngatedTargetKeepsNoBundle(t *testing.T) {
	hs := newHarness(t)
	ctx := context.Background()
	hs.prodRow.RequiresTargetID = ""
	hs.saveTarget(ctx, hs.prodRow)
	hs.source.release("orders", "orders", "ada", orders)
	up := hs.shipToStage("orders")
	if b, _ := hs.c.store.BundleInfo(ctx, up.ID); b != nil {
		t.Error("a push to a target nothing requires kept its bundle")
	}
}

// D8. The prune keeps the in-flight and the newest deployed bundle per pair,
// removes the rest, and keeps an upstream bundle a promotion in flight reads.
func TestPruneKeepsOnlyWhatCouldStillBePromoted(t *testing.T) {
	hs, first := stagedApp(t)
	ctx := context.Background()
	hs.source.release("orders", "orders", "ada", []byte("PK release two"))
	second := hs.shipToStage("orders")

	// A third, approved and not yet pushed, with a bundle of its own as a
	// tick that died after keeping it would leave.
	hs.source.release("orders", "orders", "ada", []byte("PK release three"))
	w := hs.do("ada", RoleViewer, "POST", "/requests/new", url.Values{
		"app_id": {"orders"}, "deployment_id": {hs.source.deploys["orders"][0].ID}, "target_id": {"t-stage"}})
	if w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	third := hs.newest()
	hs.approve(third.ID, "grace", nil)
	_ = hs.c.store.PutBundle(ctx, third.ID, "x", []byte("x"))

	if _, err := hs.c.store.PruneBundles(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{first.ID: false, second.ID: true, third.ID: true} {
		b, _ := hs.c.store.BundleInfo(ctx, id)
		if (b != nil) != want {
			t.Errorf("bundle %s kept=%v, want %v", id, b != nil, want)
		}
	}

	// Prod's state does not decide anything: the newest Stage bundle survives
	// a deployed promotion of it, because a failed Prod build raised again
	// needs the same bytes.
	_, _ = hs.c.store.pool.Exec(ctx, `UPDATE mc_requests SET status='cancelled' WHERE id=$1`, third.ID)
	if w := hs.promote(second.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	prom := hs.newest()
	hs.approve(prom.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})
	hs.tick()
	hs.tick()
	if got := hs.request(prom.ID); got.Status != StatusDeployed {
		t.Fatalf("the promotion ended %s: %s", got.Status, got.Failure)
	}
	if b, _ := hs.c.store.BundleInfo(ctx, second.ID); b == nil {
		t.Error("the newest Stage bundle was pruned once Prod deployed")
	}
}

// D12 and D13: parity is recorded after the promotion deploys; a difference
// holds the next promotion until a reviewer who did not raise it acknowledges.
func TestADifferenceHoldsTheNextPromotionUntilAcknowledged(t *testing.T) {
	hs, up := stagedApp(t)
	hs.prod.nextSBOM = sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.1", "npm:b@2.0.0")
	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	prom := hs.newest()
	hs.approve(prom.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})
	hs.tick()
	hs.tick()
	checks, _ := hs.c.store.Checks(context.Background(), prom.ID)
	parity := CheckOf(checks, CheckParity)
	if parity == nil || parity.Status != CheckDiffers || !strings.Contains(parity.Report, "changed npm:a 1.0.0 → 1.0.1") {
		t.Fatalf("parity: %+v", parity)
	}
	mailed := false
	for _, m := range hs.mails {
		mailed = mailed || strings.Contains(m, "built different packages")
	}
	if !mailed {
		t.Error("the administrators were not told about the difference")
	}

	// A new release through Stage cannot go to Prod while the difference stands.
	hs.source.release("orders", "orders", "ada", []byte("PK release two"))
	next := hs.shipToStage("orders")
	if w := hs.promote(next.ID, nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "acknowledge") {
		t.Fatalf("the hold did not hold: %s", refusal(w))
	}

	path := "/requests/" + prom.ID + "/checks/" + parity.ID + "/acknowledge"
	if w := hs.do("ada", RoleViewer, "POST", path, url.Values{"reason": {"mine"}}); w.Code != http.StatusForbidden {
		t.Errorf("the requester, not a reviewer, acknowledged: %d", w.Code)
	}
	if err := hs.c.store.AddReviewer(context.Background(), Reviewer{ID: "rv3", SubjectType: "user", SubjectID: "ada", SubjectName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if w := hs.do("ada", RoleViewer, "POST", path, url.Values{"reason": {"mine"}}); w.Code != http.StatusForbidden {
		t.Errorf("a reviewer acknowledged a difference on their own promotion: %d", w.Code)
	}
	deps := CheckOf(checks, CheckDependencies)
	if w := hs.do("grace", RoleViewer, "POST", "/requests/"+prom.ID+"/checks/"+deps.ID+"/acknowledge",
		url.Values{"reason": {"x"}}); w.Code != http.StatusConflict {
		t.Errorf("acknowledging a dependencies check: %d", w.Code)
	}
	if w := hs.do("grace", RoleViewer, "POST", "/requests/"+prom.ID+"/checks/"+parity.ID+"/waive",
		url.Values{"reason": {"x"}}); w.Code != http.StatusConflict {
		t.Errorf("waiving a parity check: %d", w.Code)
	}
	if w := hs.do("grace", RoleViewer, "POST", path, url.Values{"reason": {"a patch bump, read the changelog"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("acknowledging: %s", refusal(w))
	}
	if w := hs.do("linus", RoleViewer, "POST", path, url.Values{"reason": {"again"}}); w.Code != http.StatusConflict {
		t.Errorf("acknowledging twice: %d", w.Code)
	}
	if w := hs.promote(next.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("the hold did not lift: %s", refusal(w))
	}
}

// D11. On a target requiring locked dependencies, a failing check blocks
// approval until a reviewer who did not raise it waives it.
func TestAnUnlockedReleaseWaitsForAWaiver(t *testing.T) {
	hs := newHarness(t)
	ctx := context.Background()
	hs.prodRow.RequiresLockfile = true
	hs.saveTarget(ctx, hs.prodRow)
	hs.source.release("orders", "orders", "ada", orders)
	hs.stage.nextSBOM = sbomFixture(unlockedPy, "python 3.12.4", "PyPI:flask@3.0.0")
	up := hs.shipToStage("orders")
	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	prom := hs.newest()
	checks, _ := hs.c.store.Checks(ctx, prom.ID)
	lock := CheckOf(checks, CheckLockfile)
	if lock == nil || lock.Status != CheckFail {
		t.Fatalf("lockfile check: %+v", lock)
	}
	w := hs.do("grace", RoleViewer, "POST", "/requests/"+prom.ID+"/approve", url.Values{"owner_group_id": {"g-prod"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("approval with a failing lockfile check: %s", refusal(w))
	}
	if w := hs.do("grace", RoleViewer, "POST", "/requests/"+prom.ID+"/checks/"+lock.ID+"/waive", url.Values{}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("a waiver with no reason: %d", w.Code)
	}
	if w := hs.do("grace", RoleViewer, "POST", "/requests/"+prom.ID+"/checks/"+lock.ID+"/waive",
		url.Values{"reason": {"Python, until the platform reads hashes"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("waiving: %s", refusal(w))
	}
	hs.approve(prom.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})
	hs.tick()
	if got := hs.request(prom.ID); got.Status != StatusDeploying {
		t.Fatalf("the waived promotion is %s: %s", got.Status, got.Failure)
	}
}

// D2. Stage cannot be removed while Prod requires it, and the refusal says so.
func TestARequiredTargetCannotBeRemoved(t *testing.T) {
	hs := newHarness(t)
	w := hs.do("ada", RoleEditor, "POST", "/targets/t-stage/delete", url.Values{})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Prod requires this target") {
		t.Fatalf("removing Stage: %s", refusal(w))
	}
}

// D5. Send newest on a gated target sends what Stage has, not the source's newest.
func TestSendNewestOnAGatedTargetSendsStagesRelease(t *testing.T) {
	hs, up := stagedApp(t)
	ctx := context.Background()
	hs.prodRow.FastTrack = true
	hs.saveTarget(ctx, hs.prodRow)
	// A first promotion makes the link the fast-track needs.
	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	first := hs.newest()
	hs.approve(first.ID, "grace", url.Values{"owner_group_id": {"g-prod"}})
	hs.tick()
	hs.tick()
	link, _ := hs.c.store.FindLink(ctx, "t-prod", "orders")
	if link == nil {
		t.Fatal("no link after the first promotion")
	}

	staged := hs.shipToStage("orders") // the same release, sent again
	hs.source.release("orders", "orders", "ada", []byte("PK newest on dev, never staged"))

	w := hs.do("grace", RoleViewer, "POST", "/links/"+link.ID+"/send", url.Values{"reason": {"hotfix"}, "confirm": {"Prod"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("send newest: %s", refusal(w))
	}
	sent := hs.newest()
	if sent.PromotedFrom != staged.ID || sent.SourceDeploymentID != staged.SourceDeploymentID {
		t.Errorf("send newest sent %s (from %q), want Stage's %s", sent.SourceDeploymentID, sent.PromotedFrom, staged.SourceDeploymentID)
	}
}

// The pages a promotion adds render with every check status, so a wrong field
// cannot abort a request page mid-tag.
func TestPromotionPagesRender(t *testing.T) {
	hs, up := stagedApp(t)
	for _, path := range []string{"/requests/" + up.ID, "/requests/new?promote=" + up.ID, "/targets", "/apps/orders"} {
		w := hs.do("grace", RoleEditor, "GET", path, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: %s", path, refusal(w))
		}
		if strings.Contains(w.Body.String(), "<no value>") {
			t.Errorf("%s rendered <no value>", path)
		}
	}
	w := hs.do("ada", RoleViewer, "GET", "/requests/"+up.ID, nil)
	if !strings.Contains(w.Body.String(), "Promote to Prod") {
		t.Error("a deployed Stage request does not offer the promotion")
	}
	w = hs.do("ada", RoleEditor, "GET", "/targets", nil)
	if !strings.Contains(w.Body.String(), "Stage → Prod") {
		t.Error("the Targets page does not show the chain")
	}
}

// A failed build on the target is recorded with the tail of its log, read from
// /logs: the platform's deployment reads carry no build_log, and a failure
// with no reason was what the first run of the staged plan recorded.
func TestAFailedBuildRecordsItsLog(t *testing.T) {
	hs := newHarness(t)
	hs.source.release("orders", "orders", "ada", orders)
	hs.stage.failNext = "==> Installing\nERROR: app did not become ready"
	w := hs.do("ada", RoleViewer, "POST", "/requests/new", url.Values{
		"app_id": {"orders"}, "deployment_id": {hs.source.deploys["orders"][0].ID}, "target_id": {"t-stage"}})
	if w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	req := hs.newest()
	hs.approve(req.ID, "grace", url.Values{"owner_group_id": {"g-stage"}})
	hs.tick()
	hs.tick()
	got := hs.request(req.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Failure, "did not become ready") {
		t.Fatalf("the failed build recorded %s: %q", got.Status, got.Failure)
	}
}

// A promotion into a target with a default owning group lands owned by that
// group, with no group chosen at approval.
func TestAPromotionIsOwnedByTheTargetsGroup(t *testing.T) {
	hs, up := stagedApp(t)
	ctx := context.Background()
	hs.prodRow.DefaultOwnerGroupID, hs.prodRow.DefaultOwnerGroupName = "g-prod", "prod owners"
	hs.saveTarget(ctx, hs.prodRow)
	if w := hs.promote(up.ID, nil); w.Code != http.StatusSeeOther {
		t.Fatal(refusal(w))
	}
	prom := hs.newest()
	hs.approve(prom.ID, "grace", nil)
	hs.tick()
	got := hs.request(prom.ID)
	if got.OwnerGroupID != "g-prod" || got.Status != StatusDeploying {
		t.Fatalf("the promotion is %s, owned by %q", got.Status, got.OwnerGroupID)
	}
	if a := hs.prod.apps[got.TargetAppID]; a == nil || a.OwnerGroupID != "g-prod" {
		t.Errorf("the app on Prod is not owned by Prod's group: %+v", a)
	}
}
