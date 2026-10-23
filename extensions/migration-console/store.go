package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- Targets ----------------------------------------------------------------

const targetCols = `id, kind, label, base_url, deploy_key_enc, key_prefix,
	default_owner_group_id, default_owner_group_name, fast_track,
	created_by_id, created_by_name, created_at, last_tested_at, last_test_ok, last_test_result, apps_url`

func scanTarget(row pgx.Row) (*Target, error) {
	var t Target
	err := row.Scan(&t.ID, &t.Kind, &t.Label, &t.BaseURL, &t.DeployKeyEnc, &t.KeyPrefix,
		&t.DefaultOwnerGroupID, &t.DefaultOwnerGroupName, &t.FastTrack,
		&t.CreatedByID, &t.CreatedByName, &t.CreatedAt, &t.LastTestedAt, &t.LastTestOK, &t.LastTestResult, &t.AppsURL)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+targetCols+` FROM mc_targets ORDER BY kind DESC, label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) GetTarget(ctx context.Context, id string) (*Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx, `SELECT `+targetCols+` FROM mc_targets WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// SourceTarget is the one row describing this install. Absent until an
// administrator adds it, which is one of the four steps /health names.
func (s *Store) SourceTarget(ctx context.Context) (*Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx,
		`SELECT `+targetCols+` FROM mc_targets WHERE kind=$1 LIMIT 1`, TargetKindSource))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// SaveTarget writes a target row, and **carries the test the caller just
// took**.
//
// Every write of this row goes through handleSaveTarget, which tests the
// connection before saving and refuses to save at all if that test fails — so
// the three test columns on the struct it hands over describe a test that
// happened seconds ago. The upsert used to drop them on the conflict branch,
// leaving RecordTargetTest as the sole writer, and the visible result was a row
// that had just been proved reachable showing the age of the *previous* test.
// The assignment in the handler was dead code and the page was telling a stale
// truth; there is no reading of "one writer" worth either.
func (s *Store) SaveTarget(ctx context.Context, t *Target) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_targets (`+targetCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			label=EXCLUDED.label, base_url=EXCLUDED.base_url,
			deploy_key_enc=EXCLUDED.deploy_key_enc, key_prefix=EXCLUDED.key_prefix,
			default_owner_group_id=EXCLUDED.default_owner_group_id,
			default_owner_group_name=EXCLUDED.default_owner_group_name,
			fast_track=EXCLUDED.fast_track,
			last_tested_at=EXCLUDED.last_tested_at,
			last_test_ok=EXCLUDED.last_test_ok,
			last_test_result=EXCLUDED.last_test_result,
			apps_url=EXCLUDED.apps_url`,
		t.ID, t.Kind, t.Label, t.BaseURL, t.DeployKeyEnc, t.KeyPrefix,
		t.DefaultOwnerGroupID, t.DefaultOwnerGroupName, t.FastTrack,
		t.CreatedByID, t.CreatedByName, t.CreatedAt, t.LastTestedAt, t.LastTestOK, t.LastTestResult, t.AppsURL)
	return err
}

// RecordTargetTest writes a test's outcome. appsURL is what the test learned
// about where that install serves apps; empty keeps what was known, so a target
// that failed to answer does not lose it.
func (s *Store) RecordTargetTest(ctx context.Context, id string, ok bool, result, appsURL string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE mc_targets SET last_tested_at=now(), last_test_ok=$2, last_test_result=$3,
			apps_url=COALESCE(NULLIF($4, ''), apps_url) WHERE id=$1`,
		id, ok, result, appsURL)
	return err
}

func (s *Store) SetTargetAppsURL(ctx context.Context, id, appsURL string) error {
	_, err := s.pool.Exec(ctx, `UPDATE mc_targets SET apps_url=$2 WHERE id=$1`, id, appsURL)
	return err
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM mc_targets WHERE id=$1`, id)
	return err
}

// --- Reviewers --------------------------------------------------------------

func (s *Store) ListReviewers(ctx context.Context) ([]Reviewer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, subject_type, subject_id, subject_name, added_by_name, added_at
		FROM mc_reviewers ORDER BY subject_type, subject_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reviewer
	for rows.Next() {
		var r Reviewer
		if err := rows.Scan(&r.ID, &r.SubjectType, &r.SubjectID, &r.SubjectName,
			&r.AddedByName, &r.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddReviewer(ctx context.Context, r Reviewer) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_reviewers (id, subject_type, subject_id, subject_name, added_by_name)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (subject_type, subject_id) DO NOTHING`,
		r.ID, r.SubjectType, r.SubjectID, r.SubjectName, r.AddedByName)
	return err
}

func (s *Store) RemoveReviewer(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM mc_reviewers WHERE id=$1`, id)
	return err
}

// --- Links ------------------------------------------------------------------

const linkCols = `id, target_id, source_app_id, source_app_slug, source_app_name,
	target_app_id, target_app_slug, target_visibility, last_deployment_id, created_at, created_by_name`

func scanLink(row pgx.Row) (*Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.TargetID, &l.SourceAppID, &l.SourceAppSlug, &l.SourceAppName,
		&l.TargetAppID, &l.TargetAppSlug, &l.TargetVisibility, &l.LastDeploymentID,
		&l.CreatedAt, &l.CreatedByName)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *Store) ListLinks(ctx context.Context) ([]Link, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+linkCols+` FROM mc_links ORDER BY source_app_name, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (s *Store) FindLink(ctx context.Context, targetID, sourceAppID string) (*Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx,
		`SELECT `+linkCols+` FROM mc_links WHERE target_id=$1 AND source_app_id=$2`, targetID, sourceAppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// SaveLink writes the binding on the first successful deploy of a pair, and
// afterwards records what that pair now looks like on the far side.
//
// The slug is carried rather than fixed: an approver who renamed the app on its
// way over renamed the destination, and the next request through this pair
// starts from that name.
func (s *Store) SaveLink(ctx context.Context, l *Link) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_links (`+linkCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (target_id, source_app_id) DO UPDATE SET
			target_app_id=EXCLUDED.target_app_id,
			target_app_slug=EXCLUDED.target_app_slug,
			target_visibility=EXCLUDED.target_visibility,
			last_deployment_id=EXCLUDED.last_deployment_id,
			source_app_slug=EXCLUDED.source_app_slug,
			source_app_name=EXCLUDED.source_app_name`,
		l.ID, l.TargetID, l.SourceAppID, l.SourceAppSlug, l.SourceAppName,
		l.TargetAppID, l.TargetAppSlug, l.TargetVisibility, l.LastDeploymentID,
		l.CreatedAt, l.CreatedByName)
	return err
}

func (s *Store) DeleteLink(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM mc_links WHERE id=$1`, id)
	return err
}

// --- Requests ---------------------------------------------------------------

const requestCols = `id, seq, status, target_id, source_app_id, source_app_slug, source_app_name,
	source_deployment_id, source_release_label, requester_id, requester_name, requester_email,
	reason, note, destination_slug, destination_visibility, owner_group_id, owner_group_name,
	target_app_id, notify_requester, scheduled_for, via, decided_by_id, decided_by_name,
	decided_at, decision_reason, drift_note, bundle_sha256, bundle_bytes, target_deployment_id,
	failure, created_at, updated_at`

func scanRequest(row pgx.Row) (*Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.Seq, &r.Status, &r.TargetID, &r.SourceAppID, &r.SourceAppSlug,
		&r.SourceAppName, &r.SourceDeploymentID, &r.SourceReleaseLabel, &r.RequesterID,
		&r.RequesterName, &r.RequesterEmail, &r.Reason, &r.Note, &r.DestinationSlug,
		&r.DestinationVisibility, &r.OwnerGroupID, &r.OwnerGroupName, &r.TargetAppID,
		&r.NotifyRequester, &r.ScheduledFor, &r.Via, &r.DecidedByID, &r.DecidedByName,
		&r.DecidedAt, &r.DecisionReason, &r.DriftNote, &r.BundleSHA256, &r.BundleBytes,
		&r.TargetDeploymentID, &r.Failure, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetRequest(ctx context.Context, id string) (*Request, error) {
	r, err := scanRequest(s.pool.QueryRow(ctx, `SELECT `+requestCols+` FROM mc_requests WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (s *Store) queryRequests(ctx context.Context, where string, args ...any) ([]Request, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+requestCols+` FROM mc_requests `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) ListRequests(ctx context.Context) ([]Request, error) {
	return s.queryRequests(ctx, ``)
}

func (s *Store) RequestsForApp(ctx context.Context, appID string) ([]Request, error) {
	return s.queryRequests(ctx, `WHERE source_app_id=$1`, appID)
}

// RequestsVisibleTo is every request a person may open: the ones they raised,
// and the ones somebody added them to.
func (s *Store) RequestsVisibleTo(ctx context.Context, userID string) ([]Request, error) {
	return s.queryRequests(ctx, `
		WHERE requester_id=$1
		   OR id IN (SELECT request_id FROM mc_request_viewers WHERE user_id=$1)`, userID)
}

func (s *Store) DueRequests(ctx context.Context, now time.Time) ([]Request, error) {
	return s.queryRequests(ctx, `
		WHERE status=$1 AND (scheduled_for IS NULL OR scheduled_for <= $2)`, StatusApproved, now)
}

func (s *Store) InFlightRequests(ctx context.Context) ([]Request, error) {
	return s.queryRequests(ctx, `WHERE status=$1`, StatusDeploying)
}

func (s *Store) InsertRequest(ctx context.Context, r *Request) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_requests (id, status, target_id, source_app_id, source_app_slug, source_app_name,
			source_deployment_id, source_release_label, requester_id, requester_name, requester_email,
			reason, note, destination_slug, destination_visibility, owner_group_id, owner_group_name,
			target_app_id, notify_requester, via)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		r.ID, r.Status, r.TargetID, r.SourceAppID, r.SourceAppSlug, r.SourceAppName,
		r.SourceDeploymentID, r.SourceReleaseLabel, r.RequesterID, r.RequesterName, r.RequesterEmail,
		r.Reason, r.Note, r.DestinationSlug, r.DestinationVisibility, r.OwnerGroupID, r.OwnerGroupName,
		r.TargetAppID, r.NotifyRequester, r.Via)
	return err
}

// SetStatus writes the denormalised status column **in the same transaction as
// the event that caused it**, which is the one rule that makes the column safe
// to have at all.
//
// Deriving status from the latest event on every read is correct and unusable —
// the request list is this app's home page — and letting the two be written
// apart is how a request ends up `approved` with no approval in its history.
func (s *Store) SetStatus(ctx context.Context, requestID, status string, ev Event, fields map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	set := []string{"status=$2", "updated_at=now()"}
	args := []any{requestID, status}
	for col, v := range fields {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	if _, err := tx.Exec(ctx, `UPDATE mc_requests SET `+strings.Join(set, ", ")+` WHERE id=$1`, args...); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetStatusIf is SetStatus with a precondition: the row moves only if it is
// still in the status the caller believed it was.
//
// It exists because two things race for one request and only one of them is a
// person. The tick claims an approved request, downloads a release archive that
// may be tens of megabytes, and *then* writes `deploying` with the digest — so
// there is a window, sometimes a long one, in which somebody pressing Cancel
// and a push that is already fetching bytes are both about to write the same
// row. Last-write-wins there is the worst of both: the request reads
// `cancelled` and the bundle goes to the target anyway, or it reads `deploying`
// and the person who stopped it is told nothing.
//
// So the move is conditional and its result is *reported*: the tick asks to go
// approved → deploying, and a false answer means somebody cancelled underneath
// it, which aborts the push before a single byte is offered to the far side.
// The event is written only when the row actually moved, because the ledger's
// rule is that a status and the event explaining it are one transaction.
func (s *Store) SetStatusIf(ctx context.Context, requestID, from, to string, ev Event, fields map[string]any) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	set := []string{"status=$3", "updated_at=now()"}
	args := []any{requestID, from, to}
	for col, v := range fields {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	tag, err := tx.Exec(ctx,
		`UPDATE mc_requests SET `+strings.Join(set, ", ")+` WHERE id=$1 AND status=$2`, args...)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// UpdateRequestFields is the same shape without a status move — an edited note,
// a changed destination slug — and still writes its event in the transaction.
func (s *Store) UpdateRequestFields(ctx context.Context, requestID string, ev Event, fields map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if len(fields) > 0 {
		set := []string{"updated_at=now()"}
		args := []any{requestID}
		for col, v := range fields {
			args = append(args, v)
			set = append(set, fmt.Sprintf("%s=$%d", col, len(args)))
		}
		if _, err := tx.Exec(ctx, `UPDATE mc_requests SET `+strings.Join(set, ", ")+` WHERE id=$1`, args...); err != nil {
			return err
		}
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- Request viewers --------------------------------------------------------

func (s *Store) ListRequestViewers(ctx context.Context, requestID string) ([]RequestViewer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT request_id, user_id, user_name, user_email, added_by_name, added_at
		FROM mc_request_viewers WHERE request_id=$1 ORDER BY user_name`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestViewer
	for rows.Next() {
		var v RequestViewer
		if err := rows.Scan(&v.RequestID, &v.UserID, &v.UserName, &v.UserEmail,
			&v.AddedByName, &v.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) AddRequestViewer(ctx context.Context, v RequestViewer, ev Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO mc_request_viewers (request_id, user_id, user_name, user_email, added_by_name)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (request_id, user_id) DO NOTHING`,
		v.RequestID, v.UserID, v.UserName, v.UserEmail, v.AddedByName); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveRequestViewer(ctx context.Context, requestID, userID string, ev Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM mc_request_viewers WHERE request_id=$1 AND user_id=$2`, requestID, userID); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AppsSomeoneWasAddedTo is the second half of "they may request future
// migrations": every source app this person has been named on, whether or not
// they own it on Toft.
func (s *Store) AppsSomeoneWasAddedTo(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT r.source_app_id
		FROM mc_request_viewers v JOIN mc_requests r ON r.id = v.request_id
		WHERE v.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// KnownPeople is everybody this console has a name for, and it is **derived
// from the ledger rather than kept as a directory**.
//
// The console holds a read-scoped `toftd_` key, and a deploy key of either scope
// may read the *group* directory and never the user one — names, counts and
// addresses, never a roster. So there is no list of people to offer, and every
// form that wanted one asked for a raw UUID typed by hand: adding a reviewer,
// naming somebody on a request, and choosing the group that will own an app on
// the far side.
//
// Two of those three can be answered honestly without asking the platform for
// anything it will not give. Everyone who has ever raised, decided or been
// named on a request is already recorded here, with the name and address the
// platform gave at the time — so the union of those columns is a directory of
// *the people who have taken part*, which is who a change board is usually
// adding. It is offered as a suggestion and never as the whole world: the id
// field still accepts anything, because somebody's first involvement cannot
// have been recorded before it happened.
//
// Most recent wins, so a person who changed their display name reads as the
// name they have now.
func (s *Store) KnownPeople(ctx context.Context) ([]Person, error) {
	rows, err := s.pool.Query(ctx, `
		WITH seen AS (
			SELECT requester_id AS id, requester_name AS name, requester_email AS email, created_at AS at
			  FROM mc_requests WHERE requester_id <> ''
			UNION ALL
			SELECT user_id, user_name, user_email, added_at
			  FROM mc_request_viewers WHERE user_id <> ''
			UNION ALL
			SELECT decided_by_id, decided_by_name, '', decided_at
			  FROM mc_requests WHERE decided_by_id <> '' AND decided_at IS NOT NULL
			UNION ALL
			SELECT subject_id, subject_name, '', added_at
			  FROM mc_reviewers WHERE subject_type = 'user'
		)
		SELECT DISTINCT ON (id) id, name, email
		  FROM seen ORDER BY id, at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Label()) < strings.ToLower(out[j].Label())
	})
	return out, nil
}

// --- App notes --------------------------------------------------------------

func (s *Store) AppNote(ctx context.Context, appID string) (*AppNote, error) {
	var n AppNote
	err := s.pool.QueryRow(ctx, `
		SELECT source_app_id, body, updated_by_id, updated_by_name, updated_at
		FROM mc_app_notes WHERE source_app_id=$1`, appID).
		Scan(&n.SourceAppID, &n.Body, &n.UpdatedByID, &n.UpdatedByName, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) SaveAppNote(ctx context.Context, n AppNote, ev Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO mc_app_notes (source_app_id, body, updated_by_id, updated_by_name, updated_at)
		VALUES ($1,$2,$3,$4, now())
		ON CONFLICT (source_app_id) DO UPDATE SET
			body=EXCLUDED.body, updated_by_id=EXCLUDED.updated_by_id,
			updated_by_name=EXCLUDED.updated_by_name, updated_at=now()`,
		n.SourceAppID, n.Body, n.UpdatedByID, n.UpdatedByName); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- Attachments ------------------------------------------------------------

const attachmentCols = `id, scope, COALESCE(request_id,''), source_app_id, filename, content_type,
	size_bytes, sha256, uploaded_by_id, uploaded_by_name, uploaded_at`

func scanAttachment(row pgx.Row) (*Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.Scope, &a.RequestID, &a.SourceAppID, &a.Filename, &a.ContentType,
		&a.SizeBytes, &a.SHA256, &a.UploadedByID, &a.UploadedByName, &a.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) listAttachments(ctx context.Context, where string, args ...any) ([]Attachment, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+attachmentCols+` FROM mc_attachments `+where+` ORDER BY uploaded_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) RequestAttachments(ctx context.Context, requestID string) ([]Attachment, error) {
	return s.listAttachments(ctx, `WHERE scope=$1 AND request_id=$2`, AttachRequest, requestID)
}

func (s *Store) AppAttachments(ctx context.Context, appID string) ([]Attachment, error) {
	return s.listAttachments(ctx, `WHERE scope=$1 AND source_app_id=$2`, AttachApp, appID)
}

func (s *Store) GetAttachment(ctx context.Context, id string) (*Attachment, []byte, error) {
	var a Attachment
	var body []byte
	err := s.pool.QueryRow(ctx, `
		SELECT `+attachmentCols+`, bytes FROM mc_attachments WHERE id=$1`, id).
		Scan(&a.ID, &a.Scope, &a.RequestID, &a.SourceAppID, &a.Filename, &a.ContentType,
			&a.SizeBytes, &a.SHA256, &a.UploadedByID, &a.UploadedByName, &a.UploadedAt, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &a, body, nil
}

func (s *Store) AddAttachment(ctx context.Context, a Attachment, body []byte, ev Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var requestID any
	if a.RequestID != "" {
		requestID = a.RequestID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO mc_attachments (id, scope, request_id, source_app_id, filename, content_type,
			size_bytes, sha256, bytes, uploaded_by_id, uploaded_by_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		a.ID, a.Scope, requestID, a.SourceAppID, a.Filename, a.ContentType,
		a.SizeBytes, a.SHA256, body, a.UploadedByID, a.UploadedByName); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteAttachment(ctx context.Context, id string, ev Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM mc_attachments WHERE id=$1`, id); err != nil {
		return err
	}
	if err := writeEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- Checks -----------------------------------------------------------------

func (s *Store) Checks(ctx context.Context, requestID string) ([]Check, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, request_id, kind, tool, status, verdict, summary, report, ran_at
		FROM mc_checks WHERE request_id=$1 ORDER BY kind`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.RequestID, &c.Kind, &c.Tool, &c.Status,
			&c.Verdict, &c.Summary, &c.Report, &c.RanAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveCheck(ctx context.Context, c Check) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_checks (id, request_id, kind, tool, status, verdict, summary, report, ran_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.ID, c.RequestID, c.Kind, c.Tool, c.Status, c.Verdict, c.Summary, c.Report, c.RanAt)
	return err
}

// --- Events -----------------------------------------------------------------

func writeEventTx(ctx context.Context, tx pgx.Tx, ev Event) error {
	var requestID any
	if ev.RequestID != "" {
		requestID = ev.RequestID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO mc_events (action, actor_id, actor_name, request_id, source_app_id, target_id, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ev.Action, ev.ActorID, ev.ActorName, requestID, ev.SourceAppID, ev.TargetID, ev.Detail)
	return err
}

// WriteEvent records an action that changed no request row — a target created, a
// connection tested, a roster change, a notification sent or failed.
func (s *Store) WriteEvent(ctx context.Context, ev Event) error {
	var requestID any
	if ev.RequestID != "" {
		requestID = ev.RequestID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_events (action, actor_id, actor_name, request_id, source_app_id, target_id, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ev.Action, ev.ActorID, ev.ActorName, requestID, ev.SourceAppID, ev.TargetID, ev.Detail)
	return err
}

func (s *Store) listEvents(ctx context.Context, where string, args ...any) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, at, action, actor_id, actor_name, COALESCE(request_id,''), source_app_id, target_id, detail
		FROM mc_events `+where+` ORDER BY id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.At, &e.Action, &e.ActorID, &e.ActorName,
			&e.RequestID, &e.SourceAppID, &e.TargetID, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RequestEvents(ctx context.Context, requestID string) ([]Event, error) {
	return s.listEvents(ctx, `WHERE request_id=$1`, requestID)
}

func (s *Store) AppEvents(ctx context.Context, appID string) ([]Event, error) {
	return s.listEvents(ctx, `WHERE source_app_id=$1`, appID)
}
