package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every page parses and renders with nothing in it.
//
// The empty case is the one a template gets wrong: a nil map indexed, a nil
// pointer dereferenced, a range over a nil slice that was actually a nil
// pointer. A freshly-installed console renders exactly this data, so a failure
// here is a failure on the first page anybody sees.
func TestEveryPageRendersEmpty(t *testing.T) {
	r := newRenderer(newURLs("/apps/migration-console"))

	empty := func(name string, data any) {
		t.Helper()
		w := httptest.NewRecorder()
		r.page(w, 200, name, View{Title: "t", Nav: "queue", Caps: map[string]bool{}, Data: data})
		body := w.Body.String()
		if body == "" {
			t.Errorf("%s rendered nothing", name)
		}
		if strings.Contains(body, "<no value>") {
			t.Errorf("%s rendered a <no value>, which means a field name is wrong", name)
		}
	}

	empty("queue", QueueData{Tab: "mine", Counts: map[string]int{}})
	empty("new", NewRequestData{})
	empty("request", RequestData{Request: Request{ID: "r1", Status: StatusSubmitted}})
	empty("app", AppData{AppID: "a1", TargetLabel: map[string]string{}})
	empty("targets", TargetsData{})
	empty("reviewers", map[string]any{"Roster": nil, "Groups": nil})
	empty("links", map[string]any{"Links": nil, "Reviewer": false})
	empty("settings", SettingsData{Settings: map[string]string{}})
	empty("health", nil)
	empty("refused", nil)
}

// The pages that carry real rows render them, and every fragment an htmx swap
// targets exists — a swap into a template that does not exist is a silent empty
// region rather than an error.
func TestPagesAndFragmentsRenderWithData(t *testing.T) {
	r := newRenderer(newURLs("/apps/mc"))
	now := time.Now()
	req := Request{ID: "r1", Seq: 7, Status: StatusDeploying, SourceAppName: "Orders",
		SourceAppSlug: "orders", SourceReleaseLabel: "main@abc123", DestinationSlug: "orders",
		RequesterName: "Ada", CreatedAt: now, BundleSHA256: strings.Repeat("a", 64), BundleBytes: 2048}

	data := RequestData{
		Request: req, MayEdit: true, MayAddPeople: true, MayDecide: true,
		Target:      &Target{ID: "t1", Label: "Production"},
		Viewers:     []RequestViewer{{UserID: "u2", UserName: "Grace", AddedByName: "Ada", AddedAt: now}},
		Attachments: []Attachment{{ID: "at1", Filename: "change.pdf", ContentType: "application/pdf", SizeBytes: 10, SHA256: strings.Repeat("b", 64), UploadedByName: "Ada", UploadedAt: now}},
		Events:      []Event{{At: now, Action: "request.submitted", ActorName: "Ada"}},
		Checks:      []Check{{Kind: "dependencies", Tool: "Toft", Verdict: "ok", Summary: "nothing known"}},
		Destination: "the existing app orders",
	}
	w := httptest.NewRecorder()
	r.page(w, 200, "request", View{Title: "r", Caps: map[string]bool{}, Data: data})
	for _, want := range []string{"Orders", "Production", "Grace", "change.pdf", "main@abc123"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the request page does not mention %q", want)
		}
	}

	// Every fragment an htmx swap targets is **executed**, not merely looked
	// up. Existing and rendering are different questions, and the difference is
	// what a wrong field name costs: `$r.IsCurrent` where the field is
	// `Current` aborts execution mid-attribute, and the region the swap lands
	// in becomes a truncated table with no control in it. A test that only
	// asked whether the block existed passed all the way through that.
	newData := map[string]any{"AppID": "a1", "WantedAny": true, "Gone": false,
		"Rows": []ReleaseRow{{
			Deployment: Deployment{ID: "d1", Status: "success", Branch: "main",
				CommitSHA: "abc1234", StartedAt: now, Current: true},
			Verdict: "ok", Why: "nothing known", Wanted: true}}}
	for _, f := range []struct {
		page, block string
		data        any
	}{
		{"request", "note", data}, {"request", "destination", data},
		{"request", "attachments", data}, {"request", "people", data},
		{"request", "progress", data},
		{"app", "appnote", AppData{AppID: "a1", TargetLabel: map[string]string{}}},
		{"app", "appattachments", AppData{AppID: "a1", TargetLabel: map[string]string{}}},
		{"queue", "palette-results", []PaletteHit{{Href: "/x", Title: "#1 · Orders", Sub: "Awaiting review"}}},
		{"new", "releases", newData},
		{"targets", "targetrow", &Target{ID: "t1", Label: "Production", Kind: TargetKindTarget}},
	} {
		if _, ok := r.pages[f.page]; !ok {
			t.Fatalf("no page %q", f.page)
		}
		if tmpl := r.pages[f.page].Lookup(f.block); tmpl == nil {
			t.Errorf("%s has no %q block, so an htmx swap into it would silently blank the region",
				f.page, f.block)
			continue
		}
		var buf bytes.Buffer
		if err := r.pages[f.page].ExecuteTemplate(&buf, f.block, f.data); err != nil {
			t.Errorf("%s/%s does not render: %v", f.page, f.block, err)
			continue
		}
		if strings.TrimSpace(buf.String()) == "" {
			t.Errorf("%s/%s rendered nothing, which a swap turns into a blank region", f.page, f.block)
		}
		if strings.Contains(buf.String(), "<no value>") {
			t.Errorf("%s/%s rendered a <no value>, which means a field name is wrong", f.page, f.block)
		}
	}
}

// The live region says something for every state a request can be in.
//
// It is polled while a push runs and swapped over the top of itself, so a
// status with no branch is not a missing sentence — it is the moment the push
// finished replacing the card with nothing at all, on exactly the two outcomes
// somebody is watching for.
func TestTheProgressRegionCoversEveryStatus(t *testing.T) {
	r := newRenderer(newURLs("/apps/mc"))
	for _, status := range []string{
		StatusSubmitted, StatusApproved, StatusDeploying, StatusDeployed,
		StatusRejected, StatusWithdrawn, StatusCancelled, StatusFailed,
	} {
		var buf bytes.Buffer
		data := RequestData{Request: Request{ID: "r1", Status: status,
			RequesterName: "Ada", DestinationSlug: "orders",
			Failure: "the target refused it", DecidedByName: "Grace"}}
		if err := r.pages["request"].ExecuteTemplate(&buf, "progress", data); err != nil {
			t.Fatalf("progress for %q: %v", status, err)
		}
		// The wrapper div is always emitted; what matters is whether anything
		// is inside it.
		if !strings.Contains(buf.String(), "chip") {
			t.Errorf("a %q request shows nothing in the live region", status)
		}
	}
}

// The base path rule, both halves. Getting these the same way round is the
// mistake the helper exists to prevent.
func TestURLsCarryThePrefixExactlyWhereTheyShould(t *testing.T) {
	u := newURLs("/apps/migration-console")
	if got := u.Path("/requests/12"); got != "/apps/migration-console/requests/12" {
		t.Errorf("a link is %q; every href, form action and htmx URL must carry the prefix", got)
	}
	if got := u.Bare("/requests/12"); got != "/requests/12" {
		t.Errorf("a Location is %q; the proxy re-adds the prefix, so it must not be there already", got)
	}
	root := newURLs("")
	if got := root.Path("/x"); got != "/x" {
		t.Errorf("with no base path a link is %q", got)
	}
}

// The controls this app grew after an end-to-end pass are **executed**, not
// merely present in the file.
//
// Every one of them replaces something that could not be reached or could not
// be typed, and each is one field name away from rendering nothing at all —
// which is the failure this suite exists for: a blank where a control should be
// is invisible, and the release picker reached production that way once
// already.
func TestTheControlsAnEndToEndPassAddedActuallyRender(t *testing.T) {
	r := newRenderer(newURLs("/apps/mc"))
	now := time.Now()

	// Cancel. Offered on an approved request and on no other, because it is the
	// one state where a decision exists and nothing has been sent.
	approved := RequestData{
		Request: Request{ID: "r1", Seq: 3, Status: StatusApproved, SourceAppName: "Orders",
			SourceAppSlug: "orders", DestinationSlug: "orders", RequesterName: "Ada",
			SourceReleaseLabel: "main@abc123", CreatedAt: now},
		Target: &Target{ID: "t1", Label: "Production"}, MayCancel: true,
	}
	var buf bytes.Buffer
	if err := r.pages["request"].ExecuteTemplate(&buf, "content",
		View{Title: "r", Caps: map[string]bool{}, Data: approved}); err != nil {
		t.Fatalf("the request page with a cancel control does not render: %v", err)
	}
	for _, want := range []string{"/requests/r1/cancel", "Cancel this migration"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("an approved request offers no %q — the one lever that stops a push", want)
		}
	}

	// The owning-group picker, and the sentence that stands in for it. The
	// second is the one that matters: a target whose license locks groups has
	// nothing to offer, and an empty select with no explanation is how that
	// arrives as a push failure minutes later on the other install.
	first := RequestData{
		Request:   Request{ID: "r2", Status: StatusSubmitted, SourceAppSlug: "orders", DestinationSlug: "orders"},
		Target:    &Target{ID: "t1", Label: "Production"},
		MayDecide: true,
		TargetGroups: []Group{
			{ID: "g1", Name: "Service Owners", Members: 3},
			{ID: "g2", Name: "Empty Team", Members: 0},
		},
	}
	buf.Reset()
	if err := r.pages["request"].ExecuteTemplate(&buf, "content",
		View{Title: "r", Caps: map[string]bool{}, Data: first}); err != nil {
		t.Fatalf("the approval form with a group picker does not render: %v", err)
	}
	for _, want := range []string{`name="owner_group_id"`, "Service Owners", "3 member(s)",
		"Empty Team", "empty, cannot own an app", "disabled"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the group picker does not mention %q", want)
		}
	}

	first.TargetGroups = nil
	first.GroupsRefusal = "Production has no groups at all."
	buf.Reset()
	if err := r.pages["request"].ExecuteTemplate(&buf, "content",
		View{Title: "r", Caps: map[string]bool{}, Data: first}); err != nil {
		t.Fatalf("the approval form with no groups does not render: %v", err)
	}
	if !strings.Contains(buf.String(), "Production has no groups at all.") {
		t.Error("a target with no groups says nothing about why — which is the failure arriving " +
			"at the push instead, on the other install, several minutes later")
	}

	// "Raise the same request again" — a prefilled link, on every ending
	// somebody would want it from, carrying the release by id.
	for _, status := range []string{StatusFailed, StatusRejected, StatusWithdrawn, StatusCancelled} {
		buf.Reset()
		data := RequestData{Request: Request{ID: "r3", Status: status, SourceAppID: "a9",
			SourceDeploymentID: "d9", TargetID: "t1", DestinationSlug: "orders",
			RequesterName: "Ada", DecidedByName: "Grace", Reason: "quarterly release"}}
		if err := r.pages["request"].ExecuteTemplate(&buf, "progress", data); err != nil {
			t.Fatalf("progress for %q: %v", status, err)
		}
		for _, want := range []string{"again=r3", "app=a9", "deployment=d9", "target=t1"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("a %s request offers no way to raise it again (%q missing) — which "+
					"leaves the requester retyping five fields from a dead request", status, want)
			}
		}
	}
	// ...and never on one that is still moving, or that already landed. Those
	// are not endings, and a "raise it again" beside a running push is an
	// invitation to send the same bytes twice.
	for _, status := range []string{StatusSubmitted, StatusApproved, StatusDeploying, StatusDeployed} {
		buf.Reset()
		data := RequestData{Request: Request{ID: "r4", Status: status, SourceAppID: "a9",
			DestinationSlug: "orders", RequesterName: "Ada"}}
		if err := r.pages["request"].ExecuteTemplate(&buf, "progress", data); err != nil {
			t.Fatalf("progress for %q: %v", status, err)
		}
		if strings.Contains(buf.String(), "again=") {
			t.Errorf("a %s request offers to raise itself again", status)
		}
	}
}

// The Links page is readable by a reviewer and writable by an editor, and the
// split is the whole reason the page admits two roles at all.
//
// Fast-track needs the roster; the roster is deliberately not the
// administrators; so a page gated on `Editor` hid the button from everyone
// entitled to press it and left the feature reachable only by breaking the rule
// it sits beside. Deleting a link is still configuration.
func TestTheLinksPageSplitsReadingFromConfiguring(t *testing.T) {
	r := newRenderer(newURLs("/apps/mc"))
	rows := []LinkState{{
		Link:  Link{ID: "l1", SourceAppID: "a1", SourceAppName: "Orders", SourceAppSlug: "orders", TargetAppSlug: "orders"},
		State: LinkBehind, TargetLabel: "Production", CheckedAt: time.Now(),
		CanSendNewest: true,
	}}

	reviewer := new(bytes.Buffer)
	if err := r.pages["links"].ExecuteTemplate(reviewer, "content", View{Title: "l",
		Caps: map[string]bool{}, Data: map[string]any{
			"Links": rows, "Reviewer": true, "Editor": false}}); err != nil {
		t.Fatalf("the links page does not render for a reviewer: %v", err)
	}
	if !strings.Contains(reviewer.String(), "/links/l1/send") {
		t.Error("a reviewer cannot see the fast-track button on the page that carries it, " +
			"which is the only place it exists")
	}
	if strings.Contains(reviewer.String(), "/links/l1/delete") {
		t.Error("a reviewer is offered Delete link, which is configuration and belongs to an editor")
	}

	editor := new(bytes.Buffer)
	if err := r.pages["links"].ExecuteTemplate(editor, "content", View{Title: "l",
		Caps: map[string]bool{}, Data: map[string]any{
			"Links": rows, "Reviewer": false, "Editor": true}}); err != nil {
		t.Fatalf("the links page does not render for an editor: %v", err)
	}
	if !strings.Contains(editor.String(), "/links/l1/delete") {
		t.Error("an editor cannot delete a link")
	}
}

// The forms that used to take a raw UUID now suggest what the console knows,
// and the suggestions are a datalist rather than a select — because the console
// holds a deploy key, a deploy key of either scope may read the group directory
// and never the user one, and a picker that cannot list everybody must not
// stop anybody being named.
func TestTheIdentityFieldsSuggestWithoutConstraining(t *testing.T) {
	r := newRenderer(newURLs("/apps/mc"))
	people := []Person{{ID: "u1", Name: "Ada Lovelace"}, {ID: "u2", Name: "Grace Hopper"}}

	var buf bytes.Buffer
	if err := r.pages["request"].ExecuteTemplate(&buf, "people", RequestData{
		Request: Request{ID: "r1"}, MayAddPeople: true, People: people}); err != nil {
		t.Fatalf("the people fragment does not render: %v", err)
	}
	for _, want := range []string{`list="knownpeople"`, "<datalist", "Ada Lovelace", `value="u2"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the add-a-person form does not offer %q", want)
		}
	}
	if strings.Contains(buf.String(), "<select id=\"uid\"") {
		t.Error("the user id is a select, which would refuse anybody this console has not met")
	}

	buf.Reset()
	if err := r.pages["reviewers"].ExecuteTemplate(&buf, "content", View{Title: "rv",
		Caps: map[string]bool{}, Data: map[string]any{
			"Roster": nil, "Groups": nil, "People": people}}); err != nil {
		t.Fatalf("the reviewers page does not render: %v", err)
	}
	if !strings.Contains(buf.String(), `list="knownpeople"`) || !strings.Contains(buf.String(), "Grace Hopper") {
		t.Error("adding a reviewer still offers nothing but a blank box for a UUID")
	}
}
