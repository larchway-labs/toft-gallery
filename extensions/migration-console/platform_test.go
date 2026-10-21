package main

import (
	"encoding/json"
	"testing"
)

// The platform's wire contract, pinned against a real response.
//
// A mistyped `json:` tag does not fail — it renders a zero value, and a release
// picker showing "1 Jan 0001 · by manual" is a picker nobody trusts. This body
// is what GET /apps/{id}/deployments actually answered, trimmed.
func TestADeploymentIsReadTheWayThePlatformWritesIt(t *testing.T) {
	const body = `{
	  "id": "62c4e516",
	  "app_id": "68e2e801",
	  "commit_sha": "011187fe0118de6c69d977b0dc8245814bf6ebfc",
	  "branch": "main",
	  "status": "success",
	  "triggered_by": "manual",
	  "execution_mode": "shared",
	  "runtime_language": "go",
	  "runtime_version": "1.27.0",
	  "started_at": "2026-08-26T20:53:08.442143Z",
	  "finished_at": "2026-08-26T20:53:10.764962Z",
	  "current": true,
	  "serving": true
	}`
	var d Deployment
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d.StartedAt.IsZero() || d.FinishedAt == nil {
		t.Fatalf("the timestamps did not land: started=%v finished=%v", d.StartedAt, d.FinishedAt)
	}
	if !d.Current {
		t.Error("the serving release did not read as current, so the picker would preselect nothing")
	}
	label := d.Label()
	for _, want := range []string{"main@011187fe", "26 Aug 2026", "by manual", "go 1.27.0"} {
		if !contains(label, want) {
			t.Errorf("the release label %q does not carry %q — a picker showing ids teaches the "+
				"submitter that they chose an app and the release was incidental", label, want)
		}
	}
	if contains(label, "0001") {
		t.Errorf("the label carries a zero time: %q", label)
	}
}

// Four states that are not the same thing, and only one of them is "clean".
// *Not recorded* is not *clean*, and a chip that said otherwise would be the
// worst possible thing on an approval screen.
func TestADependencyVerdictNeverInventsAPass(t *testing.T) {
	off := `{"mode":"off","enforcing":false,"available":false,
	  "reason":"Dependency policy is off on this install.","has_data":false,"scanned":false}`
	var r DependencyReport
	if err := json.Unmarshal([]byte(off), &r); err != nil {
		t.Fatal(err)
	}
	word, why := r.Verdict()
	if word != "" {
		t.Errorf("policy off rendered as %q; it must render as absent", word)
	}
	if !contains(why, "off on this install") {
		t.Errorf("the sentence does not carry the platform's own words: %q", why)
	}

	var nilRep *DependencyReport
	if w, _ := nilRep.Verdict(); w != "" {
		t.Errorf("an absent report rendered as %q", w)
	}

	noData := DependencyReport{Available: true, HasData: false}
	if w, why := noData.Verdict(); w != "" || !contains(why, "clean bill of health") {
		t.Errorf("a feed that never loaded rendered as %q / %q", w, why)
	}

	notScanned := DependencyReport{Available: true, HasData: true, Scanned: false}
	if w, _ := notScanned.Verdict(); w != "" {
		t.Errorf("an unscanned release rendered as %q", w)
	}

	clean := DependencyReport{Available: true, HasData: true, Scanned: true}
	clean.Serving = &struct {
		DeploymentID string `json:"deployment_id"`
		Counts       struct {
			Total    int `json:"total"`
			Malware  int `json:"malware"`
			Blocking int `json:"blocking"`
			Counted  int `json:"counted"`
			Unknown  int `json:"unknown"`
		} `json:"counts"`
		Direct int `json:"direct"`
	}{Direct: 6}
	if w, _ := clean.Verdict(); w != "clean" {
		t.Errorf("a completed scan with nothing counted rendered as %q", w)
	}
	clean.Serving.Counts.Malware = 1
	if w, _ := clean.Verdict(); w != "vulnerable" {
		t.Errorf("malware rendered as %q; there is no mode in which somebody should have to go "+
			"looking for it", w)
	}
	if tone("vulnerable") != "danger" || tone("warn") != "attention" || tone("clean") != "ok" {
		t.Error("a verdict word does not map to the tone it means")
	}
}

// The single-app read is an envelope and the list is bare rows, and reading one
// as the other yields a zero-valued app rather than an error — which is why
// they are two functions and this test exists.
func TestTheSingleAppReadIsAnEnvelope(t *testing.T) {
	const body = `{"app":{"id":"a1","slug":"orders","name":"Orders","owner_id":"u1",
	  "visibility":"private","framework":"go"},
	  "env_vars":[{"key":"DATABASE_URL","sensitive":true},{"key":"REGION","sensitive":false}]}`
	var env appEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	if env.App.Slug != "orders" || env.App.OwnerID != "u1" {
		t.Fatalf("the app did not land: %+v", env.App)
	}
	if len(env.EnvVars) != 2 {
		t.Fatalf("env names did not land: %+v", env.EnvVars)
	}
}

// Names once, values never. What goes to the target's create is every name the
// source declares with an empty value beside it — a seed for the target's
// administrator, not a copy of this install's configuration.
func TestEnvSeedCarriesNamesAndNeverValues(t *testing.T) {
	if got := EnvNamesJSON(nil); got != "" {
		t.Errorf("an app with no variables sent %q; it should send nothing at all", got)
	}
	var seed map[string]string
	if err := json.Unmarshal([]byte(EnvNamesJSON([]string{"DATABASE_URL", "REGION"})), &seed); err != nil {
		t.Fatal(err)
	}
	if len(seed) != 2 {
		t.Fatalf("seed = %v", seed)
	}
	for k, v := range seed {
		if v != "" {
			t.Errorf("%s carried a value (%q) to the target; this console synchronises names and "+
				"never values, and claiming otherwise would be a lie in a release note", k, v)
		}
	}
}

// A target that tested red says so.
//
// The chip is driven by a *bool, and a template asking `{{if .LastTestOK}}`
// gets "is the pointer set", which is true for a failure as much as for a
// success. The row that exists to tell an administrator which of the two it
// was showed green for both.
func TestATargetThatFailedItsTestDoesNotReadAsReached(t *testing.T) {
	yes, no := true, false
	if (Target{}).Reached() {
		t.Error("a target that has never been tested reads as reached")
	}
	if (Target{LastTestOK: &no}).Reached() {
		t.Error("a target whose last test failed reads as reached")
	}
	if !(Target{LastTestOK: &yes}).Reached() {
		t.Error("a target whose last test passed does not read as reached")
	}
}

// A request may pin any successful release, and the platform scans only the one
// serving. Recording the serving release's verdict on a request for a different
// one would put another release's scan on the record as this one's.
func TestAVerdictIsOnlyEverAboutTheReleaseItNames(t *testing.T) {
	body := `{"mode":"warn","available":true,"has_data":true,"scanned":true,
	  "locked_by_policy":true,
	  "serving":{"deployment_id":"d46","counts":{"total":3,"malware":1},"direct":3},
	  "refused":{"deployment_id":"d47","commit_sha":"abc","reason":"malware"}}`
	var r DependencyReport
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if !r.LockedByPolicy || r.Refused == nil || r.Refused.DeploymentID != "d47" {
		t.Fatalf("the policy's lock and refusal did not decode: %+v", r)
	}
	if !r.Describes("d46") || r.Describes("d45") {
		t.Error("Describes does not name exactly the serving release")
	}
	if w, _ := r.VerdictFor("d46"); w != "vulnerable" {
		t.Errorf("the serving release's own verdict is %q", w)
	}
	if w, why := r.VerdictFor("d45"); w != "" || !contains(why, "different one") {
		t.Errorf("an older release borrowed the serving one's verdict: %q / %q", w, why)
	}
	var nilRep *DependencyReport
	if w, _ := nilRep.VerdictFor("d1"); w != "" || nilRep.Describes("d1") {
		t.Error("an absent report described a release")
	}
}
