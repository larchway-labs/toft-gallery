package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// sbomFixture is the platform's CycloneDX as internal/api/sbom.go renders it,
// cut to what the console reads: library components with their
// `ecosystem:package@version` bom-ref, the interpreter as a `platform`
// component, and the toft:* properties that carry the platform's own claims.
func sbomFixture(props map[string]string, runtime string, libs ...string) string {
	type prop struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type comp struct {
		Type    string `json:"type"`
		BOMRef  string `json:"bom-ref"`
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	}
	doc := struct {
		BOMFormat string `json:"bomFormat"`
		Metadata  struct {
			Properties []prop `json:"properties"`
		} `json:"metadata"`
		Components   []comp `json:"components"`
		Compositions []any  `json:"compositions"`
	}{BOMFormat: "CycloneDX", Compositions: []any{}}
	doc.Metadata.Properties = []prop{{"toft:sbom_format", "1"}}
	for k, v := range props {
		doc.Metadata.Properties = append(doc.Metadata.Properties, prop{k, v})
	}
	for _, ref := range libs {
		eco, rest, _ := strings.Cut(ref, ":")
		at := strings.LastIndexByte(rest, '@')
		doc.Components = append(doc.Components, comp{Type: "library", BOMRef: ref, Name: rest[:at], Version: rest[at+1:]})
		_ = eco
	}
	if runtime != "" {
		lang, ver, _ := strings.Cut(runtime, " ")
		doc.Components = append(doc.Components, comp{Type: "platform", BOMRef: "runtime:" + lang, Name: lang, Version: ver})
	}
	if len(props) == 0 {
		// A release with an inventory and no gaps and no ecosystems claims
		// nothing per ecosystem; the root composition says complete.
		doc.Compositions = []any{map[string]any{"aggregate": "complete", "assemblies": []string{"release"}}}
	}
	out, _ := json.Marshal(doc)
	return string(out)
}

var (
	lockedNPM   = map[string]string{"toft:ecosystem:npm": "complete: the lockfile was read"}
	unlockedPy  = map[string]string{"toft:ecosystem:PyPI": "unknown: no lockfile; versions were resolved at build time and may differ from those installed"}
	notResolved = map[string]string{"toft:composition": "unknown: dependencies were not resolved for this release"}
	static      = map[string]string{"toft:composition": "complete: serves files, installs nothing"}
)

// §7 test 4. A Python release fails the lockfile check, and the summary is the
// platform's own sentence: the console holds no second definition of "locked",
// which is what would have passed a hashed requirements.txt the platform
// itself calls unlocked.
func TestPythonIsRefusedInThePlatformsWords(t *testing.T) {
	status, summary := lockfileCheck(sbomFixture(unlockedPy, "python 3.12.4", "PyPI:flask@3.0.0"), "Stage")
	if status != CheckFail {
		t.Fatalf("an unlocked Python release read %q", status)
	}
	if !strings.Contains(summary, "no lockfile; versions were resolved at build time") {
		t.Errorf("the summary is not the platform's reason: %q", summary)
	}
	if !strings.Contains(summary, "PyPI") {
		t.Errorf("the summary does not name the ecosystem: %q", summary)
	}
}

// §7 test 2, its lockfile half. Every way of having no package list fails —
// not recorded is never a pass.
func TestALockfileCheckNeverPassesWithoutAPackageList(t *testing.T) {
	for name, raw := range map[string]string{
		"nothing captured yet": "",
		"policy off":           sbomFixture(notResolved, "node 22.1.0"),
		"not json":             "<html>",
	} {
		status, summary := lockfileCheck(raw, "Stage")
		if status == CheckPass {
			t.Errorf("%s passed: %q", name, summary)
		}
	}
	_, why := lockfileCheck(sbomFixture(notResolved, ""), "Stage")
	if !strings.Contains(why, "turn on its dependency policy") {
		t.Errorf("policy off does not say how to fix it: %q", why)
	}
}

func TestALockfileCheckPassesOnThePlatformsClaim(t *testing.T) {
	if s, why := lockfileCheck(sbomFixture(lockedNPM, "node 22.1.0", "npm:left-pad@1.3.0"), "Stage"); s != CheckPass {
		t.Errorf("a locked npm release read %q: %s", s, why)
	}
	if s, why := lockfileCheck(sbomFixture(static, ""), "Stage"); s != CheckPass {
		t.Errorf("a static app read %q: %s", s, why)
	}
	mixed := map[string]string{
		"toft:ecosystem:npm": "complete: the lockfile was read",
		"toft:ecosystem:Go":  "incomplete: a committed vendor directory was not read",
	}
	s, why := lockfileCheck(sbomFixture(mixed, "go 1.25.0"), "Stage")
	if s != CheckFail || !strings.Contains(why, "Go: incomplete") || strings.Contains(why, "npm") {
		t.Errorf("one incomplete ecosystem of two read %q: %q", s, why)
	}
}

// §7 test 2, its parity half. Either side with no package list is
// not_recorded — never pass, and never differs either, so it holds nothing.
func TestParityOverNothingIsNotRecorded(t *testing.T) {
	good := sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.0")
	for name, pair := range map[string][2]string{
		"upstream empty":          {"", good},
		"downstream empty":        {good, ""},
		"upstream not resolved":   {sbomFixture(notResolved, "node 22.1.0"), good},
		"downstream not resolved": {good, sbomFixture(notResolved, "node 22.1.0")},
	} {
		status, summary, _ := parityCheck(pair[0], pair[1], "Stage", "Prod")
		if status != CheckNotRecorded {
			t.Errorf("%s: %q (%s)", name, status, summary)
		}
	}
}

func TestParity(t *testing.T) {
	base := sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.0", "npm:b@2.0.0")
	cases := []struct {
		name       string
		down       string
		up         string
		want       string
		reportHas  []string
		reportLack []string
	}{
		{name: "identical", up: base, down: base, want: CheckPass},
		{name: "one version changed", up: base,
			down: sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.1", "npm:b@2.0.0"),
			want: CheckDiffers, reportHas: []string{"changed npm:a 1.0.0 → 1.0.1"}, reportLack: []string{"npm:b"}},
		{name: "added and removed", up: base,
			down: sbomFixture(lockedNPM, "node 22.1.0", "npm:a@1.0.0", "npm:c@1.0.0"),
			want: CheckDiffers, reportHas: []string{"removed npm:b@2.0.0", "added npm:c@1.0.0"}},
		{name: "interpreter only", up: base,
			down: sbomFixture(lockedNPM, "node 22.2.0", "npm:a@1.0.0", "npm:b@2.0.0"),
			want: CheckDiffers, reportHas: []string{"Interpreter: node 22.1.0 on Stage, node 22.2.0 on Prod"}},
		{name: "static on both sides", up: sbomFixture(static, ""), down: sbomFixture(static, ""), want: CheckPass},
	}
	for _, tc := range cases {
		status, summary, report := parityCheck(tc.up, tc.down, "Stage", "Prod")
		if status != tc.want {
			t.Errorf("%s: %q, want %q (%s)", tc.name, status, tc.want, summary)
			continue
		}
		for _, w := range tc.reportHas {
			if !strings.Contains(report, w) {
				t.Errorf("%s: the report lacks %q:\n%s", tc.name, w, report)
			}
		}
		for _, w := range tc.reportLack {
			if strings.Contains(report, w) {
				t.Errorf("%s: the report names an unchanged package %q:\n%s", tc.name, w, report)
			}
		}
		// The interpreter is on its own line, apart from the packages.
		if tc.name == "interpreter only" && strings.Contains(report, "changed") {
			t.Errorf("an interpreter-only difference listed a package: %s", report)
		}
	}
}

// D1. A target cannot require itself, the source, or anything that leads back
// to it; and the source requires nothing.
func TestTheChainRefusesLoops(t *testing.T) {
	all := []Target{
		{ID: "src", Kind: TargetKindSource, Label: "Dev"},
		{ID: "qa", Kind: TargetKindTarget, Label: "QA"},
		{ID: "stage", Kind: TargetKindTarget, Label: "Stage", RequiresTargetID: "qa"},
		{ID: "prod", Kind: TargetKindTarget, Label: "Prod", RequiresTargetID: "stage"},
	}
	if why := chainRefusal(all, "prod", TargetKindTarget, "Prod", "prod"); !strings.Contains(why, "itself") {
		t.Errorf("self: %q", why)
	}
	if why := chainRefusal(all, "prod", TargetKindTarget, "Prod", "src"); why == "" {
		t.Error("a target was allowed to require the source")
	}
	if why := chainRefusal(all, "src", TargetKindSource, "Dev", "qa"); why == "" {
		t.Error("the source was allowed to require a target")
	}
	why := chainRefusal(all, "qa", TargetKindTarget, "QA", "prod")
	if !strings.Contains(why, "QA → Prod → Stage → QA") {
		t.Errorf("a loop through the chain was not named: %q", why)
	}
	if why := chainRefusal(all, "new", TargetKindTarget, "Prod 2", "stage"); why != "" {
		t.Errorf("a second target after Stage was refused: %q", why)
	}
	if got := chainOf(all, all[3]); got != "QA → Stage → Prod" {
		t.Errorf("chain = %q", got)
	}
}

// promotable's four conditions, each alone, over injected reads.
func TestEachConditionAloneRefuses(t *testing.T) {
	ctx := context.Background()
	stage := &Target{ID: "stage", Label: "Stage", Kind: TargetKindTarget}
	prod := &Target{ID: "prod", Label: "Prod", Kind: TargetKindTarget, RequiresTargetID: "stage"}
	up := &Request{ID: "u1", Seq: 12, Status: StatusDeployed, SourceAppID: "app", TargetID: stage.ID,
		TargetAppID: "stage-app", TargetDeploymentID: "sd1"}
	held := &Request{ID: "p0", Seq: 9, Status: StatusDeployed, SourceAppID: "app", TargetID: prod.ID, PromotedFrom: "u0"}

	good := func() promotionReads {
		return promotionReads{
			NewestDeployed: func(_ context.Context, app, target string) (*Request, error) {
				if target == stage.ID {
					return up, nil
				}
				return held, nil
			},
			Serving: func(context.Context, *Request) (*Deployment, error) {
				return &Deployment{ID: "sd1", Current: true}, nil
			},
			Checks: func(context.Context, string) ([]Check, error) {
				return []Check{{Kind: CheckParity, Status: CheckAcknowledged}}, nil
			},
		}
	}
	if ok, why := promotable(ctx, good(), up, "Stage", prod); !ok {
		t.Fatalf("all four held and it refused: %s", why)
	}

	notDeployed := *up
	notDeployed.Status = StatusDeploying
	if ok, _ := promotable(ctx, good(), &notDeployed, "Stage", prod); ok {
		t.Error("1: an upstream request still deploying was promotable")
	}

	older := good()
	older.NewestDeployed = func(_ context.Context, _, target string) (*Request, error) {
		if target == stage.ID {
			return &Request{ID: "u2"}, nil
		}
		return nil, nil
	}
	if ok, why := promotable(ctx, older, up, "Stage", prod); ok || !strings.Contains(why, "newer release") {
		t.Errorf("2: an older Stage request was promotable: %q", why)
	}

	moved := good()
	moved.Serving = func(context.Context, *Request) (*Deployment, error) {
		return &Deployment{ID: "sd2", CommitSHA: "feedface99", Current: true}, nil
	}
	if ok, why := promotable(ctx, moved, up, "Stage", prod); ok || !strings.Contains(why, "feedface") {
		t.Errorf("3: Stage serving another release was promotable, or the reason does not name it: %q", why)
	}
	unreachable := good()
	unreachable.Serving = func(context.Context, *Request) (*Deployment, error) { return nil, errors.New("connection refused") }
	if ok, _ := promotable(ctx, unreachable, up, "Stage", prod); ok {
		t.Error("3: an unreachable Stage was taken as serving the release")
	}

	differs := good()
	differs.Checks = func(context.Context, string) ([]Check, error) {
		return []Check{{Kind: CheckParity, Status: CheckDiffers}}, nil
	}
	if ok, why := promotable(ctx, differs, up, "Stage", prod); ok || !strings.Contains(why, "#9") {
		t.Errorf("4: an unacknowledged difference did not hold the next promotion: %q", why)
	}
	notRecorded := good()
	notRecorded.Checks = func(context.Context, string) ([]Check, error) {
		return []Check{{Kind: CheckParity, Status: CheckNotRecorded}}, nil
	}
	if ok, _ := promotable(ctx, notRecorded, up, "Stage", prod); !ok {
		t.Error("4: not_recorded held the next promotion; only differs does")
	}

	elsewhere := &Target{ID: "prod", Label: "Prod", RequiresTargetID: "qa"}
	if ok, _ := promotable(ctx, good(), up, "Stage", elsewhere); ok {
		t.Error("a request to Stage was promotable to a target that requires QA")
	}
}
