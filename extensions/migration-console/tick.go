package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// The scheduler is the platform's, not a ticker inside this process.
//
// The tick is a **job**: `[jobs.process-migrations]` in toft.toml runs this same binary as
// `migration-console tick`, every five minutes, in a process of its own. Three
// reasons, in order of weight:
//
//  1. **The schedule ships with the code.** It is declared in toft.toml and
//     arrives with every deploy, so there is no step an administrator can
//     forget and no row on the platform this app cannot see. The Automation tab
//     shows it, pauses it until the next deploy, and runs it now.
//  2. **The run is its own record.** Each tick has a log, an outcome and a
//     reason on the platform, and `notify = "failures"` tells this app's owners
//     about one that failed. A ticker in the web process would have none of
//     that, and nothing would notice it had stopped.
//  3. **The web app is free to sleep.** A job runs whether or not the console is
//     awake, so nothing here needs `never_sleep` — and nothing here pretends a
//     schedule keeps it awake, because a schedule never does (the platform's
//     idle reaper reads none).
//
// **A tick is one pass and then exit.** It watches builds already started on the
// far side, starts at most one due migration, and re-tests stale targets. A push
// whose target has to build would outlast any sensible deadline, so the tick
// starts it, writes the row, and leaves the build to be watched by a later tick.
//
// **Two ticks never overlap.** The schedule declares `overlap = "skip"`, but a
// tick also starts when a reviewer approves something to go now, and that start
// is not a schedule firing. So each tick takes a Postgres advisory lock for its
// whole pass and a second one that cannot get it exits at once, saying so. A
// push claim is conditional on the row (Store.SetStatusIf), so even without the
// lock no request could be pushed twice — the lock is what keeps two ticks from
// recording the same finished build twice and mailing about it twice.

// tickLockKey is the advisory lock a tick holds. Any constant would do; this
// one is "mctick" in ASCII, so it reads as what it is in pg_locks.
const tickLockKey int64 = 0x6d637469636b

// tickActor is what the ledger writes as who fired a tick, from the two words
// the platform hands every run. `trigger` is how it started and `role` who
// started it, and the ledger needs both: "a person, from the Automation tab" is
// a different answer to "who put release M on production" than "the schedule".

// jobName is the tick's name in toft.toml, and so what Toft shows on the
// Automation tab and in a failure mail. It says what the job does, because
// "tick" said nothing to anybody reading either.
const jobName = "process-migrations"

func tickActor(trigger, role string) string {
	who := ""
	switch role {
	case RoleOwner, RoleEditor, RoleViewer:
		who = " (" + role + ")"
	}
	switch trigger {
	case "schedule":
		return "the platform’s scheduler"
	case "ui":
		return "a person, from Toft’s Automation tab" + who
	case "cli":
		return "a person, with toft job run" + who
	case "viewer":
		return "a console user, by approving or using Process migrations now" + who
	case "app":
		return "the console itself"
	case "":
		// No TOFT_TRIGGER is a tick run by hand from a checkout, which is
		// how the job is tested and is still a production action if the
		// DATABASE_URL is production's.
		return "by hand, outside Toft"
	}
	return "the platform (" + trigger + ")"
}

// tickMain is the job's entrypoint. Its stdout is the run's log, so every line
// is written for a person reading the run afterwards, and its exit code is the
// run's outcome: non-zero only when a migration could not be started, which is
// the failure `notify = "failures"` exists to tell somebody about.
func (c *Console) tickMain(ctx context.Context) int {
	log.SetPrefix("")
	actor := tickActor(c.cfg.Trigger, c.cfg.TriggerRole)
	log.Printf("tick fired by %s", actor)

	release, ok, err := c.store.TryTickLock(ctx)
	if err != nil {
		log.Printf("could not reach the ledger: %v", err)
		return 1
	}
	if !ok {
		log.Printf("another tick holds the lock, so this one does nothing — that one is doing the work")
		return 0
	}
	defer release()

	report, failed := c.runTick(ctx, actor)
	for _, k := range []string{"watched", "started", "targets_tested", "skipped"} {
		if v, ok := report[k]; ok && v != "" {
			log.Printf("%-15s %v", k, v)
		}
	}

	// Mail is sent detached so that it can never fail a migration, and a job
	// that exits takes its goroutines with it. Wait — bounded, because a mail
	// relay that hangs must not hold the run to its deadline.
	done := make(chan struct{})
	go func() { c.sends.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		log.Printf("gave up waiting for notifications after 30s; the ledger records each one that did not go")
	}
	if failed {
		return 1
	}
	return 0
}

// recordedFailure is an error push has already written onto its request.
type recordedFailure struct{ err error }

func (r *recordedFailure) Error() string { return r.err.Error() }
func (r *recordedFailure) Unwrap() error { return r.err }

func recorded(err error) error { return &recordedFailure{err: err} }

// runTick is one pass: builds watched, one due migration started, stale targets
// re-tested. `failed` is whether a due migration could not be started.
func (c *Console) runTick(ctx context.Context, actor string) (map[string]any, bool) {
	report := map[string]any{"fired_by": actor}
	report["watched"] = c.watchInFlight(ctx, actor)
	started, skipped, failed := c.startOneDue(ctx, actor)
	report["started"] = started
	// A tick that skips writes why. The platform records a skipped schedule run
	// for exactly that reason, and this app has no more right to a silent gap
	// than the platform does.
	if skipped != "" {
		report["skipped"] = skipped
	}
	report["targets_tested"] = c.retestTargetsDaily(ctx, actor)
	return report, failed
}

// ErrJobsUnavailable is the platform's 422 jobs_unavailable: an administrator
// has switched jobs off on this install, so no tick will run — scheduled or
// otherwise — and every approval waits until they are switched back on.
var ErrJobsUnavailable = errors.New("jobs are switched off on this install")

// startTick asks the platform to run the tick now, attributed to the person on
// the request when there is one — the act-for blob is relayed, never read.
//
// It is how an approval with no time on it goes straight away rather than
// within five minutes, and what the Run the tick button on /health does. A
// refusal is recorded so /health can say it: an install with jobs switched off
// is a console where every approval waits forever, and that is the one state
// this app can see only at the moment it asks.
func (c *Console) startTick(ctx context.Context, r *http.Request) error {
	if c.cfg.AppToken == "" || c.cfg.APIURL == "" {
		return fmt.Errorf("this app has no platform token, so it can’t start processing migrations now; the five-minute schedule still runs")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.APIURL+"/api/v1/app/jobs/"+jobName+"/runs", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AppToken)
	req.Header.Set("Content-Type", "application/json")
	if r != nil {
		if blob := r.Header.Get("X-Toft-Act-For"); blob != "" {
			req.Header.Set("X-Toft-Act-For", blob)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		err := readAPIError(resp)
		var api *APIError
		if errors.As(err, &api) && api.Code == "jobs_unavailable" {
			_ = c.store.SetSetting(ctx, SettingTickRefused,
				time.Now().UTC().Format(time.RFC3339)+" "+api.Msg)
			return fmt.Errorf("%w: %s", ErrJobsUnavailable, api.Msg)
		}
		return err
	}
	_ = c.store.SetSetting(ctx, SettingTickRefused, "")
	return nil
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
		if err != nil {
			continue
		}
		if target == nil {
			c.failInFlight(ctx, &req, actor, "the target",
				"The target was removed from this console while the release was building there. Check that install for the app.")
			continue
		}
		// This runs under the tick lock, so no other tick is part-way through a
		// push: a row that is deploying with no answer from the target recorded
		// belongs to a tick that died between the claim and that answer.
		if req.TargetAppID == "" || req.TargetDeploymentID == "" {
			c.failInFlight(ctx, &req, actor, target.Label,
				"The migration stopped before the target’s answer was recorded, so it’s unknown whether "+
					target.Label+" received the release. Check for the app there before raising this again.")
			continue
		}
		tp, err := c.platformFor(target)
		if err != nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		d, err := tp.Deployment(callCtx, req.TargetAppID, req.TargetDeploymentID)
		cancel()
		if err != nil {
			var api *APIError
			switch {
			case errors.As(err, &api) && api.Status == http.StatusNotFound:
				c.failInFlight(ctx, &req, actor, target.Label,
					"The target no longer has this deployment. The app or the release was deleted there during the build.")
			case time.Since(req.UpdatedAt) > inFlightLimit:
				c.failInFlight(ctx, &req, actor, target.Label, fmt.Sprintf(
					"%s hasn’t reported on this build for %s, so the console stopped following it. Last response: %v. Check that install for the app.",
					target.Label, inFlightLimit, err))
			}
			continue
		}
		switch d.Status {
		case "success":
			moved, _ := c.store.SetStatusIf(ctx, req.ID, StatusDeploying, StatusDeployed, Event{
				Action: "push.succeeded", ActorName: actor,
				RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
				Detail: fmt.Sprintf("%s is serving release %s", req.DestinationSlug, d.Label())}, nil)
			if moved {
				c.afterSuccess(ctx, &req, d)
			}
		case "failed":
			// The platform describes a failed build through its log, so the
			// tail of that log is what the request records and what the mail
			// carries. A generic "it failed" would send somebody to the other
			// install to read the thing this app already has.
			why := lastLines(d.BuildLog, 12)
			moved, _ := c.store.SetStatusIf(ctx, req.ID, StatusDeploying, StatusFailed, Event{
				Action: "push.failed", ActorName: actor,
				RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
				Detail: why}, map[string]any{"failure": why})
			if !moved {
				continue
			}
			c.notifyFailure(ctx, &req, "A build failed on the target", why)
			c.notifyRequester(ctx, &req, "Your migration failed",
				fmt.Sprintf("The release reached %s, but the build there failed.\n\n%s", target.Label, why))
		}
	}
	return len(reqs)
}

// inFlightLimit is how long a push may go without the target saying anything
// about its build before the console stops watching it. A build reports within
// minutes; six hours of silence is a target that has gone, not a slow one.
const inFlightLimit = 6 * time.Hour

// failInFlight ends a request the console can no longer watch. It is the way
// out of `deploying` that nothing else offers — cancel refuses a push that has
// started, and raise-again is only offered for a request that ended.
func (c *Console) failInFlight(ctx context.Context, req *Request, actor, targetLabel, why string) {
	moved, err := c.store.SetStatusIf(ctx, req.ID, StatusDeploying, StatusFailed, Event{
		Action: "push.failed", ActorName: actor,
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: why}, map[string]any{"failure": why})
	if err != nil || !moved {
		return
	}
	c.notifyFailure(ctx, req, "Couldn’t follow a migration", why)
	c.notifyRequester(ctx, req, "Your migration failed",
		fmt.Sprintf("The release was sent to %s, but the console couldn’t follow its build.\n\n%s", targetLabel, why))
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
	msg := fmt.Sprintf("%s is now serving release %s as %s.", req.SourceAppName, req.SourceReleaseLabel, req.DestinationSlug)
	c.notifyRequester(ctx, req, "Your migration finished", msg)
}

// startOneDue claims one approved request whose time has come and starts it.
// `failed` is a push that could not start, which fails the run; a request
// cancelled under it is a skip, which does not.
func (c *Console) startOneDue(ctx context.Context, actor string) (started int, skipped string, failed bool) {
	due, err := c.store.DueRequests(ctx, time.Now().UTC())
	if err != nil {
		return 0, "couldn’t read the console’s database: " + err.Error(), true
	}
	if len(due) == 0 {
		return 0, "", false
	}
	// One per tick, deliberately: a tick is bounded by the job's deadline, and
	// two pushes in one run is how the second one is the thing that gets cut
	// off. The next is five minutes away.
	req := due[0]
	pushed, err := c.push(ctx, &req, actor)
	// A migration that failed and says so on its request is the request's
	// failure, reported there and to the people it concerns. It is not the
	// tick's: exiting non-zero for it sent the console's owner a second,
	// vaguer "the job failed" mail about something already handled.
	var rf *recordedFailure
	if errors.As(err, &rf) {
		return 0, fmt.Sprintf("request %s failed, and its page says why: %v", req.ID, rf.err), false
	}
	if err != nil {
		return 0, fmt.Sprintf("request %s: %v", req.ID, err), true
	}
	if !pushed {
		// Not a failure and not a silent gap: the request was cancelled while
		// this tick was fetching its bytes, and the tick reports the reason it
		// did nothing exactly as it reports every other one.
		return 0, fmt.Sprintf("request %s: cancelled while its release was being downloaded", req.ID), false
	}
	return 1, "", false
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
		return false, fmt.Errorf("this install hasn’t been added as the source")
	}
	sp, err := c.platformFor(source)
	if err != nil {
		return false, err
	}
	target, err := c.store.GetTarget(ctx, req.TargetID)
	if err != nil || target == nil {
		return false, fmt.Errorf("the target has been removed")
	}
	tp, err := c.platformFor(target)
	if err != nil {
		return false, err
	}

	// **The link decides create or version, now** — not the copy of it taken
	// when the request was raised. Two requests raised before a first migration
	// landed both copied "no link", and the second would create a second app;
	// a link an editor deleted since would still be pushed to. Approval reads
	// the link the same way, which is what decided whether a group was asked
	// for.
	req.TargetAppID = ""
	if link, _ := c.store.FindLink(ctx, req.TargetID, req.SourceAppID); link != nil {
		req.TargetAppID = link.TargetAppID
	}
	if req.TargetAppID == "" && req.OwnerGroupID == "" {
		why := "The link to the target app was deleted after approval, so this would now create a new app with no owning group. Raise the request again and the approver will choose a group."
		moved, _ := c.store.SetStatusIf(ctx, req.ID, StatusApproved, StatusFailed, Event{
			Action: "push.failed", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: why}, map[string]any{"failure": why})
		if moved {
			c.notifyRequester(ctx, req, "Your migration couldn’t start", why)
		}
		return true, recorded(errors.New(why))
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
			drift = fmt.Sprintf("Approved release %s; the source was serving %s when the migration ran.",
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
			Detail: "couldn’t download the release: " + err.Error()},
			map[string]any{"failure": err.Error()})
		if moved {
			c.notifyFailure(ctx, req, "Couldn’t download a release", err.Error())
		}
		return false, recorded(err)
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
			Detail: "the release was downloaded and then discarded because the request was no longer approved"})
		return false, nil
	}
	if drift != "" {
		_ = c.store.WriteEvent(ctx, Event{Action: "push.drift", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID, Detail: drift})
		c.notifyFailure(ctx, req, "A migration is shipping an older release", drift)
		c.notifyRequester(ctx, req, "Your migration is shipping the approved release", drift)
	}

	// From here the row reads `deploying`, and whatever happens next has to be
	// written down even if the run is being stopped: a failure write on a
	// cancelled context is a request left deploying with nothing to watch.
	wctx := context.WithoutCancel(ctx)

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
		envSeed, envSensitive := "", ""
		if detail, err := sp.AppDetail(ctx, req.SourceAppID); err == nil {
			envSeed = EnvNamesJSON(detail.EnvKeys)
			envSensitive = EnvSensitiveJSON(detail.EnvSensitive)
		}
		result, err = tp.CreateFromArchive(ctx, zip, req.SourceAppName, req.DestinationSlug,
			req.OwnerGroupID, req.DestinationVisibility, envSeed, envSensitive)
	}
	if err != nil {
		_ = c.store.SetStatus(wctx, req.ID, StatusFailed, Event{
			Action: "push.failed", ActorName: actor,
			RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
			Detail: err.Error()}, map[string]any{"failure": err.Error()})
		c.notifyFailure(wctx, req, "The target refused a migration", err.Error())
		c.notifyRequester(wctx, req, "Your migration failed", err.Error())
		return true, recorded(err)
	}

	_ = c.store.UpdateRequestFields(wctx, req.ID, Event{
		Action: "push.accepted", ActorName: actor,
		RequestID: req.ID, SourceAppID: req.SourceAppID, TargetID: req.TargetID,
		Detail: "the target accepted the bundle and is building"},
		acceptedFields(req, result))
	return true, nil
}

// acceptedFields is what a push the target accepted writes onto the request.
// The slug is the one the target reports, not the one asked for: the create is
// sent exact, so the two agree, and if a target ever answered otherwise the
// ledger and the link would follow the app that exists rather than a name.
func acceptedFields(req *Request, result *UploadResult) map[string]any {
	fields := map[string]any{
		"target_app_id":        result.AppID,
		"target_deployment_id": result.DeploymentID,
	}
	if result.AppSlug != "" && result.AppSlug != req.DestinationSlug {
		fields["destination_slug"] = result.AppSlug
		req.DestinationSlug = result.AppSlug
	}
	return fields
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
		_ = c.store.RecordTargetTest(ctx, t.ID, ok, result, t.AppsURL)
		_ = c.store.WriteEvent(ctx, Event{Action: "target.tested",
			ActorName: actor, TargetID: t.ID, Detail: result})
		if was && !ok {
			c.notifyGroup(ctx, "A migration target stopped responding",
				fmt.Sprintf("%s (%s) passed its last daily test and failed today’s.\n\n%s", t.Label, t.BaseURL, result))
		}
		tested++
	}
	return tested
}

// lastLines is the tail of a build log — what a person reads first, and all a
// notification has room for.
func lastLines(log string, n int) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
