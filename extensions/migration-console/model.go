package main

import (
	"net/url"
	"strings"
	"time"
)

// The request's states, and rejection is terminal.
//
//	draft → submitted → approved → deploying → deployed
//	                  ↘ rejected     ↑    ↘      ↘ failed
//	        withdrawn (submitter,    │    cancelled (before the push starts)
//	        before a decision)       └── scheduled_for, held by the tick
//
// A rejected request is not reopened; a new one is raised. Reopening would let
// the serving release move under a decision somebody already made, and the
// whole point of pinning the deployment is that it cannot.
//
// **Cancelled is a state and not a copy change**, which is why it is here
// rather than on the unschedule button. Dropping a schedule makes an approved
// migration due *sooner*; stopping one is a different act, it happens after a
// decision somebody already recorded, and a change board that cannot say "this
// was approved and then called off" has to reach for `withdrawn` — which is the
// requester's word for something nobody had decided yet. The two must stay
// tellable apart a year later.
const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusDeploying = "deploying"
	StatusDeployed  = "deployed"
	StatusRejected  = "rejected"
	StatusWithdrawn = "withdrawn"
	StatusCancelled = "cancelled"
	StatusFailed    = "failed"
)

// Terminal reports whether anything can still move this request.
func Terminal(status string) bool {
	switch status {
	case StatusDeployed, StatusRejected, StatusWithdrawn, StatusCancelled, StatusFailed:
		return true
	}
	return false
}

// The two target kinds. `source` is this install — one row, holding the
// read-scoped key the console pulls with.
const (
	TargetKindTarget = "target"
	TargetKindSource = "source"
)

type Target struct {
	ID                    string
	Kind                  string
	Label                 string
	BaseURL               string
	DeployKeyEnc          string
	KeyPrefix             string
	DefaultOwnerGroupID   string
	DefaultOwnerGroupName string
	FastTrack             bool
	CreatedByID           string
	CreatedByName         string
	CreatedAt             time.Time
	LastTestedAt          *time.Time
	LastTestOK            *bool
	LastTestResult        string
	// AppsURL is where this install serves apps, ending in a slash; empty until
	// a test has read it.
	AppsURL string
	// RequiresTargetID is the target a release must have deployed to first:
	// a request here is a promotion of a request that deployed there. Empty
	// for an ungated target, and always empty on the source row.
	RequiresTargetID string
	// RequiresLockfile holds a promotion here until the upstream target's SBOM
	// says every ecosystem's lockfile was read, or a reviewer waives it.
	RequiresLockfile bool
	// RequiresLabel is the required target's label, filled in for display and
	// never stored.
	RequiresLabel string
}

// Gated reports whether a request to this target must be a promotion.
func (t Target) Gated() bool { return t.RequiresTargetID != "" }

// AppURL is the address of one app on this install, or "" when the console
// has not learned where its apps live yet. Only an http(s) address is ever
// returned: it came from another install's API and goes into an href.
func (t *Target) AppURL(slug string) string {
	if t == nil || t.AppsURL == "" || slug == "" {
		return ""
	}
	if !strings.HasPrefix(t.AppsURL, "https://") && !strings.HasPrefix(t.AppsURL, "http://") {
		return ""
	}
	return t.AppsURL + url.PathEscape(slug) + "/"
}

// Reached is whether the last test of this target succeeded.
//
// A method rather than the field, and the reason is a template: LastTestOK is a
// *bool, and `{{if .LastTestOK}}` asks whether the pointer is nil — so a target
// that had just tested **red** rendered the green chip, on the one row whose
// whole job is to say which. A pointer is three states and a template can only
// see two of them; the third is spelled out here instead.
func (t Target) Reached() bool { return t.LastTestOK != nil && *t.LastTestOK }

type Reviewer struct {
	ID          string
	SubjectType string // "user" | "group"
	SubjectID   string
	SubjectName string
	AddedByName string
	AddedAt     time.Time
}

type Link struct {
	ID               string
	TargetID         string
	SourceAppID      string
	SourceAppSlug    string
	SourceAppName    string
	TargetAppID      string
	TargetAppSlug    string
	TargetVisibility string
	LastDeploymentID string
	CreatedAt        time.Time
	CreatedByName    string
}

type Request struct {
	ID                    string
	Seq                   int64
	Status                string
	TargetID              string
	SourceAppID           string
	SourceAppSlug         string
	SourceAppName         string
	SourceDeploymentID    string
	SourceReleaseLabel    string
	RequesterID           string
	RequesterName         string
	RequesterEmail        string
	Reason                string
	Note                  string
	DestinationSlug       string
	DestinationVisibility string
	OwnerGroupID          string
	OwnerGroupName        string
	TargetAppID           string
	NotifyRequester       bool
	ScheduledFor          *time.Time
	Via                   string
	DecidedByID           string
	DecidedByName         string
	DecidedAt             *time.Time
	DecisionReason        string
	DriftNote             string
	BundleSHA256          string
	BundleBytes           int64
	TargetDeploymentID    string
	Failure               string
	// PromotedFrom is the upstream request this one promotes: same app, same
	// release, same bytes, copied from that row and never from a form.
	PromotedFrom string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Bundle is a zip the console kept because another target requires the one it
// was pushed to. The bytes themselves are read only by the push that uses them.
type Bundle struct {
	RequestID string
	SHA256    string
	Size      int64
	SBOM      string
	KeptAt    time.Time
}

// PaletteHit is one line of the command palette's results, named for the same
// reason ReleaseRow is: the fragment that renders it is executed in a test, and
// a test can only execute it with the type the handler passes.
type PaletteHit struct{ Href, Title, Sub string }

// ReleaseRow is one line of the release picker, and it is a named type rather
// than one declared inside the handler so that a test can execute the fragment
// with the very struct the handler passes. The bug that cost this app its main
// flow was a template asking for a field the struct did not have; a test that
// builds its own lookalike would have gone straight past it.
type ReleaseRow struct {
	Deployment
	Label   string
	Verdict string
	Why     string
	// Wanted is the release a "raise the same request again" link named. It
	// preselects the row instead of the one serving now — the point of raising
	// it again is usually that the same bytes are still the ones wanted.
	Wanted bool
}

type RequestViewer struct {
	RequestID   string
	UserID      string
	UserName    string
	UserEmail   string
	AddedByName string
	AddedAt     time.Time
}

// Person is one entry of the console's derived directory — see
// Store.KnownPeople for why it is derived and what it deliberately is not.
type Person struct {
	ID    string
	Name  string
	Email string
}

// Label is what a picker shows. The id is the value in every case, because the
// id is the only thing the platform will match on; the name is decoration for
// the human choosing, and a person with no recorded name reads as their id
// rather than as a blank row.
func (p Person) Label() string {
	if p.Name != "" && p.Name != p.ID {
		return p.Name
	}
	return p.ID
}

type AppNote struct {
	SourceAppID   string
	Body          string
	UpdatedByID   string
	UpdatedByName string
	UpdatedAt     time.Time
}

// The two attachment scopes.
const (
	AttachRequest = "request"
	AttachApp     = "app"
)

type Attachment struct {
	ID             string
	Scope          string
	RequestID      string
	SourceAppID    string
	Filename       string
	ContentType    string
	SizeBytes      int64
	SHA256         string
	UploadedByID   string
	UploadedByName string
	UploadedAt     time.Time
}

// The check kinds, and the statuses a check can be in. `dependencies` is
// recorded and carries its verdict in Verdict; the two promotion checks carry
// their outcome in Status, because a reviewer moves it.
//
// **Not recorded is never a pass.** A check that could not be computed — no
// package list on either side — reads not_recorded, and every gate that reads
// a check reads that as not passing.
const (
	CheckDependencies = "dependencies"
	CheckLockfile     = "lockfile"
	CheckParity       = "build_parity"

	CheckRecorded     = "recorded"
	CheckPass         = "pass"
	CheckFail         = "fail"
	CheckWaived       = "waived"
	CheckDiffers      = "differs"
	CheckAcknowledged = "acknowledged"
	CheckNotRecorded  = "not_recorded"
)

type Check struct {
	ID        string
	RequestID string
	Kind      string
	Tool      string
	Status    string
	Verdict   string
	Summary   string
	Report    string
	RanAt     *time.Time
}

type Event struct {
	ID          int64
	At          time.Time
	Action      string
	ActorID     string
	ActorName   string
	RequestID   string
	SourceAppID string
	TargetID    string
	Detail      string
}

// The four words a link can be in, derived at read time and never stored.
const (
	LinkUpToDate   = "up_to_date"
	LinkBehind     = "behind"
	LinkDiverged   = "diverged"
	LinkNotChecked = "not_checked"
)

// LinkState is what the links page shows, plus when the console last looked.
// "Not checked" is the Gallery's *not recorded is not unknown* distinction kept:
// a link the console has never been able to read is not a link that is up to
// date, and a page that renders silence as agreement is the one failure this
// column cannot have.
type LinkState struct {
	Link
	TargetLabel string
	// AppURL is the migrated app on the target, or "" when the console does
	// not know where that install serves apps.
	AppURL          string
	State           string
	SourceRelease   string
	TargetRelease   string
	CheckedAt       time.Time
	Detail          string
	CanSendNewest   bool
	NewestDeployID  string
	NewestReleaseID string
}
