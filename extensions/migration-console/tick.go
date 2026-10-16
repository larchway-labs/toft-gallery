package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// The scheduler is the platform's, not a ticker inside this process.
//
// Three reasons, in order of weight:
//
//  1. **A task is an HTTP request to a path the app published.** This app
//     publishes POST /tasks/tick; the platform calls it on the schedule an
//     administrator configured. The app never names a path, a verb or a
//     schedule, and the administrator can see and change the cadence on a
//     surface that already exists.
//  2. **The firing is signed and carries X-Toft-Role: system**, with the
//     same key a page load is signed with, so this app verifies a tick exactly
//     as it verifies a viewer and refuses anything that is not the platform. A
//     ticker in the process would have no such check because it would have no
//     request.
//  3. **A schedule is what keeps this app awake.** The idle reaper skips any app
//     with an enabled scheduled task, so publishing the tick buys the exemption
//     as a side effect — which is what lets this run in shared mode. It never
//     needs `never_sleep`, which the catalog could not grant it anyway.
//
// **A tick starts work and returns.** A scheduled task runs inside the app's web
// process under the task timeout, and a push whose target has to build could
// outlast it. So the tick claims one due request, starts the push, writes the
// row, and returns; the target's build is watched on later ticks. A request
// already `deploying` is skipped, because an overrunning tick must never start
// the same push twice.
// tickActor names who fired a tick, or "" for a caller that may not.
//
// One function so the door and the ledger cannot disagree about who is allowed
// through it, and so a test can walk every word the platform mints.
func tickActor(role string) string {
	switch role {
	case RoleSystem:
		return "the platform's scheduler"
	case RoleOwner, RoleEditor:
		return "a console editor, by hand"
	}
	return ""
}

func (c *Console) handleTick(w http.ResponseWriter, r *http.Request) {
	// **Who may fire this, and why it is not `system` alone.**
	//
	// A schedule firing on its schedule carries `system`. A schedule fired by
	// hand — the Run now button on the platform's own surface — carries *the
	// person's* role, because that is what the platform sends and it is the
	// truthful thing to send. Refusing that would mean an administrator can
	// configure the schedule and can never test it, and the first thing anybody
	// does with a new schedule is press Run now.
	//
	// So both are accepted and neither is a visitor: `system` is the platform
	// acting, and `owner`/`editor` is somebody who already configures this
	// console's targets and roster. Everything else refuses, and absent,
	// unverifiable and expired are one word — `unknown` — which refuses too.
	// The ledger records which of the two it was, because "who put release M on
	// production" has to have one answer.
	actor := tickActor(verifyRole(c.cfg.RoleKey, r.Header.Get("X-Toft-Role"), c.cfg.AppID, time.Now()))
	if actor == "" {
		http.Error(w, "this path is the platform's scheduler, and a console editor firing it by hand — "+
			"nothing else reaches it", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	report := map[string]any{}

	report["fired_by"] = actor
	// Recorded before the work, so a tick that fails halfway still counts as
	// evidence that the schedule is wired up — which is the only question this
	// row is asked.
	_ = c.store.SetSetting(ctx, SettingLastTickAt, time.Now().UTC().Format(time.RFC3339))
	report["watched"] = c.watchInFlight(ctx, actor)
	started, skipped := c.startOneDue(ctx, actor)
	report["started"] = started
	// A tick that skips writes why. The platform records a skipped schedule run
	// for exactly that reason, and this app has no more right to a silent gap
	// than the platform does.
	if skipped != "" {
		report["skipped"] = skipped
	}
	report["targets_tested"] = c.retestTargetsDaily(ctx, actor)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// watchInFlight re-reads the target's own deployment for every request this
// console has already started, and finishes the row when the far side does.
func (c *Console) watchInFlight(ctx context.Context, actor string) int {
	reqs, err := c.store.InFlightRequests(ctx)
	if err != nil {
		return 0
	}
	for _, req := range reqs {
		target, err := c.store.GetTarget(ctx, req.TargetID)
		if err != nil || target == nil {
			continue
		}
		tp, err := c.platformFor(target)
		if err != nil {
			continue
		}
		d, err := tp.Deployment(ctx, req.TargetAppID, req.TargetDeploymentID)
		if err != nil {
			continue
		}
		switch d.Status {
		case "success":
			_ = c.store.SetStatus(ctx, req.ID, StatusDeployed, Event{
				Action: "push.succeeded", ActorName: actor,
				RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
				Detail: fmt.Sprintf("%s is serving release %s", req.DestinationSlug, d.Label())}, nil)
			c.afterSuccess(ctx, &req, d)
		case "failed":
			// The platform describes a failed build through its log, so the
			// tail of that log is what the request records and what the mail
			// carries. A generic "it failed" would send somebody to the other
			// install to read the thing this app already has.
			why := lastLines(d.BuildLog, 12)
			_ = c.store.SetStatus(ctx, req.ID, StatusFailed, Event{
				Action: "push.failed", ActorName: actor,
				RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
				Detail: why}, map[string]any{"failure": why})
			c.notifyFailure(ctx, &req, "The build failed on the target", why)
			c.notifyRequester(ctx, &req, "Your migration failed",
				fmt.Sprintf("The bundle reached %s and the build there failed.\n\n%s", target.Label, why))
		}
	}
	return len(reqs)
}

// afterSuccess writes the link — the binding is made by the **first successful
// deploy** of a pair and never by an intention — and carries the destination's
// slug forward so the next request through this pair starts from the name this
// one landed with.
func (c *Console) afterSuccess(ctx context.Context, req *Request, d *Deployment) {
	link, _ := c.store.FindLink(ctx, req.TargetID, req.SourceAppID)
	l := &Link{
		ID: newID(), TargetID: req.TargetID,
		SourceAppID: req.SourceAppID, SourceAppSlug: req.SourceAppSlug, SourceAppName: req.SourceAppName,
		TargetAppID: req.TargetAppID, TargetAppSlug: req.DestinationSlug,
		TargetVisibility: req.DestinationVisibility,
		LastDeploymentID: d.ID, CreatedAt: time.Now().UTC(), CreatedByName: req.DecidedByName,
	}
	if link != nil {
		l.ID, l.CreatedAt, l.CreatedByName = link.ID, link.CreatedAt, link.CreatedByName
	}
	if err := c.store.SaveLink(ctx, l); err != nil {
		log.Printf("recording the link for %s: %v", req.ID, err)
		return
	}
	if link == nil {
		_ = c.store.WriteEvent(ctx, Event{Action: "link.created", ActorName: req.DecidedByName,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: fmt.Sprintf("%s → %s", req.SourceAppSlug, req.DestinationSlug)})
	}
	c.notifyRequester(ctx, req, "Your migration finished",
		fmt.Sprintf("%s is now serving release %s as %s.", req.SourceAppName, req.SourceReleaseLabel, req.DestinationSlug))
}

// startOneDue claims one approved request whose time has come and starts it.
func (c *Console) startOneDue(ctx context.Context, actor string) (started int, skipped string) {
	due, err := c.store.DueRequests(ctx, time.Now().UTC())
	if err != nil || len(due) == 0 {
		return 0, ""
	}
	// One per tick, deliberately: a tick is bounded by the platform's task
	// timeout, and two pushes in one firing is how the second one is the thing
	// that gets cut off.
	req := due[0]
	pushed, err := c.push(ctx, &req, actor)
	if err != nil {
		return 0, fmt.Sprintf("request %s: %v", req.ID, err)
	}
	if !pushed {
		// Not a failure and not a silent gap: the request was cancelled while
		// this tick was fetching its bytes, and the tick reports the reason it
		// did nothing exactly as it reports every other one.
		return 0, fmt.Sprintf("request %s: cancelled while its release was being downloaded", req.ID)
	}
	return 1, ""
}

// push is the whole migration: pull the pinned release from the source, check
// the pin against what is serving now, and hand the bytes to the target.
//
// It answers **whether it started one**, separately from whether anything went
// wrong, because those became two questions the moment a migration could be
// called off: a request cancelled while its archive was coming down is not a
// failed push and it is not a completed one, and a tick that reported it as
// either would be lying about the same row in opposite directions.
func (c *Console) push(ctx context.Context, req *Request, actor string) (bool, error) {
	source, err := c.store.SourceTarget(ctx)
	if err != nil || source == nil {
		return false, fmt.Errorf("this install has not been added as the source")
	}
	sp, err := c.platformFor(source)
	if err != nil {
		return false, err
	}
	target, err := c.store.GetTarget(ctx, req.TargetID)
	if err != nil || target == nil {
		return false, fmt.Errorf("the target row is gone")
	}
	tp, err := c.platformFor(target)
	if err != nil {
		return false, err
	}

	// **The pin is re-checked at execution, and drift is told rather than
	// obeyed.** It proceeds because the pin *is* the approval: shipping the
	// newer code would ship something nobody read. It does not proceed
	// silently, because a migration that lands three-day-old code is a fact the
	// people watching production need at the time, not in a post-mortem. And it
	// does not refuse, because refusing would turn an ordinary week of
	// development into a failed migration and teach everyone to schedule
	// nothing.
	drift := ""
	if deploys, err := sp.Deployments(ctx, req.SourceAppID); err == nil {
		if newest := newestSuccess(deploys); newest != nil && newest.ID != req.SourceDeploymentID {
			drift = fmt.Sprintf("pinned release %s, serving release %s at execution",
				req.SourceReleaseLabel, newest.Label())
		}
	}

	// **The window this download opens is the one a cancel has to survive.** An
	// archive can be tens of megabytes and this is where the seconds go, so
	// every status write from here to the hand-off is conditional on the
	// request still being `approved` — see Store.SetStatusIf. Unconditional
	// writes here would let a push that somebody stopped record itself as
	// failed, or start, after the row already said cancelled.
	zip, err := sp.Archive(ctx, req.SourceAppID, req.SourceDeploymentID)
	if err != nil {
		moved, _ := c.store.SetStatusIf(ctx, req.ID, StatusApproved, StatusFailed, Event{
			Action: "push.failed", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: "the release could not be downloaded: " + err.Error()},
			map[string]any{"failure": err.Error()})
		if moved {
			c.notifyFailure(ctx, req, "A release could not be downloaded", err.Error())
		}
		return false, err
	}
	sum := sha256.Sum256(zip)
	digest := hex.EncodeToString(sum[:])

	fields := map[string]any{
		"bundle_sha256": digest,
		"bundle_bytes":  int64(len(zip)),
		"drift_note":    drift,
	}
	// The claim. Nothing has been offered to the target yet, and this is the
	// last moment at which nothing has: if the row is no longer `approved`,
	// somebody cancelled while the bytes were coming down, and the right answer
	// is to drop them.
	claimed, err := c.store.SetStatusIf(ctx, req.ID, StatusApproved, StatusDeploying, Event{
		Action: "push.started", ActorName: actor,
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: fmt.Sprintf("%s, %s, sha256 %s", req.SourceReleaseLabel, humanBytes(int64(len(zip))), digest[:12])},
		fields)
	if err != nil {
		return false, err
	}
	if !claimed {
		_ = c.store.WriteEvent(ctx, Event{Action: "push.abandoned", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: "the release was downloaded and then discarded: this request stopped being approved while it was coming down"})
		return false, nil
	}
	if drift != "" {
		_ = c.store.WriteEvent(ctx, Event{Action: "push.drift", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID, Detail: drift})
		c.notifyFailure(ctx, req, "A migration ran against a superseded release", drift)
		c.notifyRequester(ctx, req, "Your migration is shipping the release it was approved for", drift)
	}

	var result *UploadResult
	if req.TargetAppID != "" {
		result, err = tp.PushVersion(ctx, req.TargetAppID, zip, req.DestinationSlug, req.DestinationVisibility)
	} else {
		// The env var *names* the source app declares, with empty values for
		// the target's administrator to fill in. A deploy key may not PATCH the
		// app, so no later request can touch them, and configuration drift on
		// the target is the target's business.
		//
		// Best effort: a source that cannot be re-read is not a reason to
		// refuse a migration whose bytes are already in hand.
		envSeed := ""
		if detail, err := sp.AppDetail(ctx, req.SourceAppID); err == nil {
			envSeed = EnvNamesJSON(detail.EnvKeys)
		}
		result, err = tp.CreateFromArchive(ctx, zip, req.SourceAppName, req.DestinationSlug,
			req.OwnerGroupID, req.DestinationVisibility, envSeed)
	}
	if err != nil {
		_ = c.store.SetStatus(ctx, req.ID, StatusFailed, Event{
			Action: "push.failed", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: err.Error()}, map[string]any{"failure": err.Error()})
		c.notifyFailure(ctx, req, "A push was refused by the target", err.Error())
		c.notifyRequester(ctx, req, "Your migration failed", err.Error())
		return true, err
	}

	_ = c.store.UpdateRequestFields(ctx, req.ID, Event{
		Action: "push.accepted", ActorName: actor,
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: "the target accepted the bundle and is building"},
		map[string]any{
			"target_app_id":        result.AppID,
			"target_deployment_id": result.DeploymentID,
		})
	return true, nil
}

// retestTargetsDaily re-tests every target once a day, and a target that goes
// from green to red is one of the failures that mails the administrators'
// group. Told once, on the transition — deduplicated by the fact that a red
// target stays red and only the change is reported.
func (c *Console) retestTargetsDaily(ctx context.Context, actor string) int {
	targets, err := c.store.ListTargets(ctx)
	if err != nil {
		return 0
	}
	tested := 0
	for _, t := range targets {
		if t.LastTestedAt != nil && time.Since(*t.LastTestedAt) < 24*time.Hour {
			continue
		}
		was := t.Reached()
		ok, result := c.testTarget(ctx, &t)
		_ = c.store.RecordTargetTest(ctx, t.ID, ok, result)
		_ = c.store.WriteEvent(ctx, Event{Action: "target.tested",
			ActorName: actor, TargetID: t.ID, Detail: result})
		if was && !ok {
			c.notifyGroup(ctx, "A migration target has stopped working",
				fmt.Sprintf("%s (%s) tested green yesterday and red today.\n\n%s", t.Label, t.BaseURL, result))
		}
		tested++
	}
	return tested
}

var _ = context.Background
var _ = http.StatusOK

// lastLines is the tail of a build log — what a person reads first, and all a
// notification has room for.
func lastLines(log string, n int) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
