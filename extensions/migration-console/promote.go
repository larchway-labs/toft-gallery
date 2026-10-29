package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Staged promotion: a target may require another, and a request to it is a
// promotion of a request that deployed there.
//
// **The ordering is the easy half.** The work is proving sameness across a gap
// the console does not control: between a Stage deploy and a Prod push sit a
// review and perhaps a scheduled window, while Dev prunes the release, somebody
// may redeploy the Stage app, and Prod builds the source again on its own day.
// So everything the feature guarantees is captured when Stage succeeds — the
// bytes (mc_bundles) and Stage's own SBOM — and checked against live state
// when Prod is pushed.
//
// What is here is the logic, kept free of the database and the network where it
// can be, so the tests can hold every condition on its own.

// chainRefusal is why a target cannot require `requires`, or "" when it can.
// `id` is the row being saved (possibly new), `kind` its kind, and `all` every
// row as stored, which the walk reads with the saved row's new requirement in
// place of its old one.
func chainRefusal(all []Target, id, kind, label, requires string) string {
	if requires == "" {
		return ""
	}
	if kind == TargetKindSource {
		return "This install is the source, and nothing is promoted to it. Only a target can require another."
	}
	if requires == id {
		return label + " can’t require itself."
	}
	byID := map[string]Target{}
	for _, t := range all {
		byID[t.ID] = t
	}
	req, ok := byID[requires]
	if !ok {
		return "That target doesn’t exist. Choose one from the list."
	}
	if req.Kind == TargetKindSource {
		return "The source can’t be required: releases leave it through a request, not a promotion. Choose a target."
	}
	// Walk upstream from the required target. Reaching the row being saved is
	// a loop, and the refusal names the whole of it so the editor sees which
	// setting to clear.
	chain := []string{label, req.Label}
	seen := map[string]bool{id: true}
	for cur := req; cur.RequiresTargetID != ""; {
		if cur.RequiresTargetID == id {
			chain = append(chain, label)
			return "That would make a loop: " + strings.Join(chain, " → ") + ". Clear one of those requirements first."
		}
		if seen[cur.ID] {
			break
		}
		seen[cur.ID] = true
		next, ok := byID[cur.RequiresTargetID]
		if !ok {
			break
		}
		chain = append(chain, next.Label)
		cur = next
	}
	return ""
}

// chainOf is a target's chain read from the top, "QA → Stage → Prod", or ""
// for a target nothing gates.
func chainOf(all []Target, t Target) string {
	if t.RequiresTargetID == "" {
		return ""
	}
	byID := map[string]Target{}
	for _, x := range all {
		byID[x.ID] = x
	}
	labels := []string{t.Label}
	seen := map[string]bool{t.ID: true}
	for cur := t; cur.RequiresTargetID != "" && !seen[cur.RequiresTargetID]; {
		next, ok := byID[cur.RequiresTargetID]
		if !ok {
			break
		}
		seen[next.ID] = true
		labels = append([]string{next.Label}, labels...)
		cur = next
	}
	return strings.Join(labels, " → ")
}

// --- The four conditions ----------------------------------------------------

// promotionReads is what promotable needs to know, injected so that each
// condition can be held on its own in a test with no server behind it.
type promotionReads struct {
	// NewestDeployed is Store.NewestDeployed.
	NewestDeployed func(ctx context.Context, sourceAppID, targetID string) (*Request, error)
	// Serving is the deployment the upstream target's app is serving now,
	// read live from that install; nil with no error when nothing is.
	Serving func(ctx context.Context, upstream *Request) (*Deployment, error)
	// Checks is Store.Checks.
	Checks func(ctx context.Context, requestID string) ([]Check, error)
}

// promotable answers whether `upstream` may be promoted to `target` now, and
// if not, the sentence a person reads. Called at raise, at approval and at the
// push, and **the push is the call that counts**: the other two save a
// reviewer from reading a request that cannot run.
//
//  1. The upstream request deployed.
//  2. It is the newest deployed request for its (source app, target), so an
//     older Stage release is never promoted past a newer one.
//  3. The upstream target is still serving exactly that deployment — read
//     live, because somebody may have redeployed the Stage app directly.
//  4. The newest deployed promotion of this app to *this* target is not
//     holding a parity difference nobody has acknowledged.
func promotable(ctx context.Context, rd promotionReads, upstream *Request, upLabel string, target *Target) (bool, string) {
	if upstream == nil {
		return false, "The request this promotes no longer exists."
	}
	if target == nil {
		return false, "The target has been removed."
	}
	if target.RequiresTargetID != upstream.TargetID {
		return false, fmt.Sprintf("%s doesn’t take releases from %s. Promote a request that deployed to the target %s requires.",
			target.Label, upLabel, target.Label)
	}
	if upstream.Status != StatusDeployed {
		return false, fmt.Sprintf("Request #%d to %s hasn’t deployed (it’s %s), so there’s nothing to promote yet.",
			upstream.Seq, upLabel, strings.ToLower(statusLabel(upstream.Status)))
	}
	newest, err := rd.NewestDeployed(ctx, upstream.SourceAppID, upstream.TargetID)
	if err != nil {
		return false, "Couldn’t read the console’s records: " + err.Error()
	}
	if newest == nil || newest.ID != upstream.ID {
		return false, fmt.Sprintf("A newer release of this app has deployed to %s since. Promote that one.", upLabel)
	}
	serving, err := rd.Serving(ctx, upstream)
	if err != nil {
		return false, fmt.Sprintf("Couldn’t read what %s is serving: %v", upLabel, err)
	}
	if serving == nil {
		return false, fmt.Sprintf("%s isn’t serving any release of this app now. Send the release to %s again.", upLabel, upLabel)
	}
	if serving.ID != upstream.TargetDeploymentID {
		return false, fmt.Sprintf("%s is now serving %s, not the release request #%d deployed there. Send that release to %s again.",
			upLabel, serving.Label(), upstream.Seq, upLabel)
	}
	held, err := rd.NewestDeployed(ctx, upstream.SourceAppID, target.ID)
	if err != nil {
		return false, "Couldn’t read the console’s records: " + err.Error()
	}
	if held != nil && held.PromotedFrom != "" {
		checks, err := rd.Checks(ctx, held.ID)
		if err != nil {
			return false, "Couldn’t read the console’s records: " + err.Error()
		}
		if c := CheckOf(checks, CheckParity); c != nil && c.Status == CheckDiffers {
			return false, fmt.Sprintf("The last promotion of this app to %s, request #%d, built different packages from %s, and a reviewer has to acknowledge the difference first.",
				target.Label, held.Seq, upLabel)
		}
	}
	return true, ""
}

// --- Reading an SBOM --------------------------------------------------------

// sbomView is the part of the platform's CycloneDX the console reads. The
// platform decides what a release's packages are and whether its lockfile was
// read; this only reads that decision back.
type sbomView struct {
	// Libraries maps each library component's bom-ref,
	// `ecosystem:package@version`, to its ecosystem, package and version.
	Libraries map[string]sbomLib
	// Runtime is the interpreter, "python 3.12.4", or "".
	Runtime string
	// Ecosystems is each `toft:ecosystem:<eco>` property: "complete: the
	// lockfile was read", or `unknown`/`incomplete` with the platform's reason.
	Ecosystems map[string]string
	// Composition is `toft:composition`, present when the release as a whole
	// was judged at once: a static app, or one nothing was resolved for.
	Composition string
	// Overall is the release's aggregate word: complete, incomplete or unknown.
	Overall string
}

type sbomLib struct{ Ecosystem, Package, Version string }

// parseSBOM reads the platform's SBOM. An empty document is an error: no SBOM
// is not an SBOM that lists nothing.
func parseSBOM(raw string) (*sbomView, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("no SBOM was recorded")
	}
	var doc struct {
		Metadata struct {
			Properties []struct{ Name, Value string } `json:"properties"`
		} `json:"metadata"`
		Components []struct {
			Type    string `json:"type"`
			BOMRef  string `json:"bom-ref"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"components"`
		Compositions []struct {
			Aggregate  string   `json:"aggregate"`
			Assemblies []string `json:"assemblies"`
		} `json:"compositions"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("the SBOM couldn’t be read: %v", err)
	}
	v := &sbomView{Libraries: map[string]sbomLib{}, Ecosystems: map[string]string{}}
	for _, p := range doc.Metadata.Properties {
		switch {
		case strings.HasPrefix(p.Name, "toft:ecosystem:"):
			v.Ecosystems[strings.TrimPrefix(p.Name, "toft:ecosystem:")] = p.Value
		case p.Name == "toft:composition":
			v.Composition = p.Value
		}
	}
	for _, c := range doc.Components {
		switch c.Type {
		case "library":
			eco := c.BOMRef
			if i := strings.IndexByte(eco, ':'); i > 0 {
				eco = eco[:i]
			} else {
				eco = ""
			}
			ref := c.BOMRef
			if ref == "" {
				ref = eco + ":" + c.Name + "@" + c.Version
			}
			v.Libraries[ref] = sbomLib{Ecosystem: eco, Package: c.Name, Version: c.Version}
		case "platform":
			if strings.HasPrefix(c.BOMRef, "runtime:") {
				v.Runtime = strings.TrimSpace(c.Name + " " + c.Version)
			}
		}
	}
	switch {
	case v.Composition != "":
		v.Overall = aggregateWord(v.Composition)
	case len(v.Ecosystems) > 0:
		v.Overall = "complete"
		for _, claim := range v.Ecosystems {
			v.Overall = worseAggregate(v.Overall, aggregateWord(claim))
		}
	default:
		v.Overall = "unknown"
		for _, c := range doc.Compositions {
			if len(c.Assemblies) == 1 && c.Assemblies[0] == "release" {
				v.Overall = c.Aggregate
			}
		}
	}
	return v, nil
}

// Unresolved reports whether the platform resolved nothing for this release —
// its dependency policy was off, or it could not scan on deploy — which is the
// one SBOM that says nothing about packages at all.
func (v *sbomView) Unresolved() bool {
	return v == nil || strings.Contains(v.Composition, "not resolved")
}

func aggregateWord(claim string) string {
	if i := strings.IndexByte(claim, ':'); i > 0 {
		return strings.TrimSpace(claim[:i])
	}
	return strings.TrimSpace(claim)
}

func worseAggregate(a, b string) string {
	rank := map[string]int{"complete": 0, "incomplete": 1, "unknown": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// --- The lockfile check -----------------------------------------------------

// lockfileCheck reads "were this release's dependencies locked?" out of the
// upstream target's SBOM, and the answer is **the platform's, not the
// console's**: `complete: the lockfile was read` per ecosystem, or a static
// app the platform calls complete as a whole. Anything else fails, in the
// platform's own words, and so does an SBOM that is missing or resolved
// nothing — **not recorded is never a pass**.
func lockfileCheck(raw, upLabel string) (status, summary string) {
	if strings.TrimSpace(raw) == "" {
		return CheckFail, fmt.Sprintf("%s’s package list isn’t recorded yet, so this can’t be checked. The console reads it again on each run; raise this again once it has, or a reviewer can waive it.", upLabel)
	}
	v, err := parseSBOM(raw)
	if err != nil {
		return CheckFail, err.Error()
	}
	if v.Unresolved() {
		return CheckFail, fmt.Sprintf("%s records no package list; turn on its dependency policy, with scanning on deploy.", upLabel)
	}
	if v.Composition != "" {
		if v.Overall == "complete" {
			return CheckPass, "Installs no packages: " + strings.TrimSpace(strings.TrimPrefix(v.Composition, "complete:"))
		}
		return CheckFail, v.Composition
	}
	if len(v.Ecosystems) == 0 {
		if v.Overall == "complete" {
			return CheckPass, "Installs no packages."
		}
		return CheckFail, fmt.Sprintf("%s’s SBOM makes no claim about this release’s packages.", upLabel)
	}
	ecos := make([]string, 0, len(v.Ecosystems))
	for e := range v.Ecosystems {
		ecos = append(ecos, e)
	}
	sort.Strings(ecos)
	var failing []string
	for _, e := range ecos {
		if aggregateWord(v.Ecosystems[e]) != "complete" {
			failing = append(failing, e+": "+v.Ecosystems[e])
		}
	}
	if len(failing) > 0 {
		return CheckFail, strings.Join(failing, "\n")
	}
	return CheckPass, "The lockfile was read for " + strings.Join(ecos, ", ") + "."
}

// --- Build parity -----------------------------------------------------------

// parityCheck compares what the downstream target installed with what the
// upstream one did, from the two installs' own SBOMs.
//
// Library components compare as a set of `ecosystem:package@version`, and the
// interpreter on its own line. Any difference is `differs`, with a report
// listing each pair as added, removed or changed. Either side having resolved
// nothing is `not_recorded` — never a pass, and never a hold either, because a
// hold on a fact nobody has is a hold nobody can lift by fixing anything.
//
// What it proves is **versions, not bytes**: the SBOM records no hashes.
func parityCheck(upRaw, downRaw, upLabel, downLabel string) (status, summary, report string) {
	up, upErr := parseSBOM(upRaw)
	down, downErr := parseSBOM(downRaw)
	switch {
	case upErr != nil:
		return CheckNotRecorded, fmt.Sprintf("Not checked: %s’s package list wasn’t recorded (%v).", upLabel, upErr), ""
	case downErr != nil:
		return CheckNotRecorded, fmt.Sprintf("Not checked: %s’s package list couldn’t be read (%v).", downLabel, downErr), ""
	case up.Unresolved():
		return CheckNotRecorded, fmt.Sprintf("Not checked: %s records no package list (%s).", upLabel, up.Composition), ""
	case down.Unresolved():
		return CheckNotRecorded, fmt.Sprintf("Not checked: %s records no package list (%s).", downLabel, down.Composition), ""
	}

	lines := packageDiff(up, down)
	if up.Runtime != down.Runtime {
		lines = append(lines, fmt.Sprintf("Interpreter: %s on %s, %s on %s",
			firstNonEmpty(up.Runtime, "none recorded"), upLabel, firstNonEmpty(down.Runtime, "none recorded"), downLabel))
	}
	if len(lines) == 0 {
		what := fmt.Sprintf("%d packages", len(up.Libraries))
		if len(up.Libraries) == 1 {
			what = "1 package"
		}
		if up.Runtime != "" {
			what += " on " + up.Runtime
		}
		return CheckPass, fmt.Sprintf("%s installed the same %s as %s.", downLabel, what, upLabel), ""
	}
	report = strings.Join(lines, "\n")
	summary = fmt.Sprintf("%s built %d difference(s) from %s.", downLabel, len(lines), upLabel)
	shown := lines
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("and %d more", len(lines)-3))
	}
	return CheckDiffers, summary + "\n" + strings.Join(shown, "\n"), report
}

// packageDiff lists the library differences between two SBOMs, one line per
// package: changed when a package has one version on each side, added or
// removed otherwise. Sorted, so the same two builds always read the same.
func packageDiff(up, down *sbomView) []string {
	type side struct{ up, down []string }
	byPkg := map[string]*side{}
	key := func(l sbomLib) string { return l.Ecosystem + ":" + l.Package }
	for ref, l := range up.Libraries {
		if _, ok := down.Libraries[ref]; ok {
			continue
		}
		s := byPkg[key(l)]
		if s == nil {
			s = &side{}
			byPkg[key(l)] = s
		}
		s.up = append(s.up, l.Version)
	}
	for ref, l := range down.Libraries {
		if _, ok := up.Libraries[ref]; ok {
			continue
		}
		s := byPkg[key(l)]
		if s == nil {
			s = &side{}
			byPkg[key(l)] = s
		}
		s.down = append(s.down, l.Version)
	}
	pkgs := make([]string, 0, len(byPkg))
	for k := range byPkg {
		pkgs = append(pkgs, k)
	}
	sort.Strings(pkgs)
	var out []string
	for _, k := range pkgs {
		s := byPkg[k]
		sort.Strings(s.up)
		sort.Strings(s.down)
		switch {
		case len(s.up) == 1 && len(s.down) == 1:
			out = append(out, fmt.Sprintf("changed %s %s → %s", k, s.up[0], s.down[0]))
		default:
			for _, v := range s.up {
				out = append(out, fmt.Sprintf("removed %s@%s", k, v))
			}
			for _, v := range s.down {
				out = append(out, fmt.Sprintf("added %s@%s", k, v))
			}
		}
	}
	return out
}

// --- Wired to the console ---------------------------------------------------

// reads is promotionReads over this console's ledger and the installs it holds
// keys for.
func (c *Console) reads() promotionReads {
	return promotionReads{
		NewestDeployed: c.store.NewestDeployed,
		Checks:         c.store.Checks,
		Serving: func(ctx context.Context, upstream *Request) (*Deployment, error) {
			t, err := c.store.GetTarget(ctx, upstream.TargetID)
			if err != nil {
				return nil, err
			}
			if t == nil {
				return nil, fmt.Errorf("that target has been removed from this console")
			}
			p, err := c.platformFor(t)
			if err != nil {
				return nil, err
			}
			callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			ds, err := p.Deployments(callCtx, upstream.TargetAppID)
			if err != nil {
				return nil, err
			}
			for i := range ds {
				if ds[i].Current {
					return &ds[i], nil
				}
			}
			return nil, nil
		},
	}
}

// promotableNow is promotable for one request against the live installs.
func (c *Console) promotableNow(ctx context.Context, upstream *Request, target *Target) (bool, string) {
	upLabel := "the required target"
	if upstream != nil {
		if t, _ := c.store.GetTarget(ctx, upstream.TargetID); t != nil {
			upLabel = t.Label
		}
	}
	return promotable(ctx, c.reads(), upstream, upLabel, target)
}

// keepsBundles reports whether a push to this target keeps its zip: only when
// another target requires it, so a console with no gated target stores nothing.
func (c *Console) keepsBundles(ctx context.Context, targetID string) (bool, error) {
	ts, err := c.store.TargetsRequiring(ctx, targetID)
	return len(ts) > 0, err
}
