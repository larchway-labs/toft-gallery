package main

import (
	"fmt"
	"os"
	"strings"
)

// The environment this app is handed, and the two variables an administrator
// supplies.
//
// The Toft contract half — PORT, HOST, BASE_PATH, and the TOFT_*
// block — is set by the platform on every start and is never configured by a
// person. The two an administrator does supply, DATABASE_URL and
// TARGET_KEY_SECRET, are declared in the catalog entry as sensitive, so the
// deploy dialog asks for them and the platform stores them encrypted under its
// own key ring. The catalog *asked*; it granted nothing (ADR §28).
type Config struct {
	Port     string
	Host     string
	BasePath string

	// DatabaseURL names the operator's Postgres. There is no embedded mode and
	// no file on disk: this app's ledger is an audit record of production
	// deploys, and putting a record somewhere you already intend to leave is
	// how it stops being one.
	DatabaseURL string
	// TargetKeySecret encrypts each target's deploy key before the row is
	// written. What it defends against is a database dump — a backup, a
	// replica, a misconfigured grant — which is a real exposure surface
	// precisely because the database is somebody else's. What it does *not*
	// defend against is a compromise of this process, which holds the secret in
	// its own environment by construction. The asymmetry is the whole value and
	// it is stated rather than implied.
	TargetKeySecret string

	// The platform's own address, from TOFT_API_URL. The app never has to
	// be told where the install it runs on is.
	APIURL string
	// AppToken is this app's outbound credential — used for /api/v1/app/self,
	// /api/v1/app/viewer, /api/v1/app/notify/email and starting the tick job.
	// It is not a deploy key and reaches nothing an ordinary app cannot reach.
	AppToken string
	AppID    string
	AppSlug  string
	// RoleKey verifies X-Toft-Role. Per app, per start, derived and never
	// stored. Absent means every request reads as `unknown`, which refuses —
	// the right direction to fail in.
	RoleKey string
	// The job's half of the contract, set only when this binary is running as
	// the `tick` job. A job holds no app token — a run that could start runs
	// could fan out — so JobToken is what it sends mail with: the platform
	// accepts it on the notify routes while the run is in flight, and nowhere
	// else. Trigger says *how* the run started and TriggerRole *who*.
	JobToken    string
	Trigger     string
	TriggerRole string
}

func loadConfig() (Config, error) {
	c := Config{
		Port:            envOr("PORT", "8080"),
		Host:            envOr("HOST", "127.0.0.1"),
		BasePath:        strings.TrimRight(os.Getenv("BASE_PATH"), "/"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		TargetKeySecret: strings.TrimSpace(os.Getenv("TARGET_KEY_SECRET")),
		APIURL:          strings.TrimRight(os.Getenv("TOFT_API_URL"), "/"),
		AppToken:        os.Getenv("TOFT_APP_TOKEN"),
		AppID:           os.Getenv("TOFT_APP_ID"),
		AppSlug:         os.Getenv("TOFT_APP_SLUG"),
		RoleKey:         os.Getenv("TOFT_ROLE_KEY"),
		JobToken:        os.Getenv("TOFT_JOB_TOKEN"),
		Trigger:         os.Getenv("TOFT_TRIGGER"),
		TriggerRole:     os.Getenv("TOFT_TRIGGER_ROLE"),
	}
	// The two the app cannot run without, refused at boot rather than at the
	// first request. An app that starts and then 500s on every page is harder
	// to diagnose than one that says what it is missing in its own log.
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required: this app keeps its ledger in the operator's Postgres and has no embedded mode")
	}
	if c.TargetKeySecret == "" {
		return c, fmt.Errorf("TARGET_KEY_SECRET is required: it encrypts each target's deploy key before the row is written")
	}
	return c, nil
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
