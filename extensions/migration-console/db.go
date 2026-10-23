package main

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// Store is the console's ledger and configuration, in the operator's Postgres.
//
// pgx, and every dependency in this repository is pure Go, because a Toft
// Go build runs on the platform host and that image carries no C compiler: a
// package importing C fails at `go build` with a message that says nothing
// about Debian and nothing about CGO. pgx is the driver Toft itself uses
// for the same reason.
type Store struct {
	pool *pgxpool.Pool
}

// TryTickLock takes the tick's advisory lock on a connection of its own, or
// reports that another tick holds it. The lock is session-scoped, so it lives
// exactly as long as that connection, and a tick killed at its deadline drops
// it with the connection rather than leaving it held.
func (s *Store) TryTickLock(ctx context.Context) (release func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, tickLockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, tickLockKey)
		conn.Release()
	}, true, nil
}

func openStore(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DATABASE_URL: %w", err)
	}
	// Small: this app serves a queue and a detail page, and the operator's
	// database is shared with whatever else they run in it.
	cfg.MaxConns = 8
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reaching the database: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("applying the schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// --- Settings ---------------------------------------------------------------

// The console's own settings, one row per key. A map rather than a struct
// because every one of them is optional and an install that has set none is the
// ordinary state of a freshly-deployed extension.
const (
	SettingAdminGroupID     = "admin_group_id"
	SettingAdminGroupName   = "admin_group_name"
	SettingAdminGroupEmail  = "admin_group_email"
	SettingNotifyByDefault  = "notify_requester_by_default"
	SettingAttachmentMaxMB  = "attachment_max_mb"
	SettingPrivateRangesOK  = "private_ranges_allowed"
	SettingRequireNoteOnNew = "require_note_on_new_request"
	// SettingTickRefused is written when the platform refused to start the
	// tick with jobs_unavailable, as "<RFC 3339 time> <the platform's words>",
	// and cleared by the next start it accepts. It is not configuration: it is
	// the one sighting this app gets of jobs being switched off install-wide,
	// which is the state that stops every migration and that no other call
	// reports.
	SettingTickRefused = "tick_refused"
)

func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM mc_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mc_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}
