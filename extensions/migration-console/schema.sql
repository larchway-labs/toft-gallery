-- The migration console's own schema, in the operator's Postgres.
--
-- Applied at boot, idempotently. There is no migration tool here on purpose:
-- this is one app's private schema in a database it was given, and a second
-- migration framework beside Toft's would be a thing to keep in step for
-- no benefit. Every statement is CREATE ... IF NOT EXISTS or an ALTER guarded
-- the same way, so a restart is a no-op and an upgrade adds what is new.
--
-- Everything is prefixed mc_ because the operator may well have pointed this at
-- a database that already holds something else.

CREATE TABLE IF NOT EXISTS mc_settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- A target is an install this console can push to, and one row of a different
-- kind is *this* install, holding the read-scoped key the console pulls with.
-- Storing it beside the others is deliberate: there is one place where the
-- console's credentials live, one place to look after an incident, and no
-- second concept for "the local one".
CREATE TABLE IF NOT EXISTS mc_targets (
    id                       text PRIMARY KEY,
    kind                     text NOT NULL DEFAULT 'target',  -- 'target' | 'source'
    label                    text NOT NULL,
    base_url                 text NOT NULL,
    deploy_key_enc           text NOT NULL,
    key_prefix               text NOT NULL DEFAULT '',
    default_owner_group_id   text NOT NULL DEFAULT '',
    default_owner_group_name text NOT NULL DEFAULT '',
    -- Off by default, and per target: an install can run UAT with one click and
    -- production with two, which is the shape most change boards actually have.
    fast_track               boolean NOT NULL DEFAULT false,
    created_by_id            text NOT NULL DEFAULT '',
    created_by_name          text NOT NULL DEFAULT '',
    created_at               timestamptz NOT NULL DEFAULT now(),
    last_tested_at           timestamptz,
    last_test_ok             boolean,
    last_test_result         text NOT NULL DEFAULT ''
);

-- The roster. Two authorities, not one: X-Toft-Role decides configure vs
-- submit, and this decides who reviews. Collapsing them would make every
-- approver able to add a target pointing wherever they liked.
CREATE TABLE IF NOT EXISTS mc_reviewers (
    id            text PRIMARY KEY,
    subject_type  text NOT NULL,          -- 'user' | 'group'
    subject_id    text NOT NULL,
    subject_name  text NOT NULL,
    added_by_name text NOT NULL DEFAULT '',
    added_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (subject_type, subject_id)
);

-- (target, source app) -> target app. Written only on the first successful
-- deploy of that pair, deleted and remade rather than re-pointed.
--
-- target_app_slug carries the destination's name forward: an approver who
-- renamed the app on its way over is renaming it for every later request too,
-- which is what makes the slug a property of the destination rather than of one
-- push.
CREATE TABLE IF NOT EXISTS mc_links (
    id                 text PRIMARY KEY,
    target_id          text NOT NULL REFERENCES mc_targets(id) ON DELETE CASCADE,
    source_app_id      text NOT NULL,
    source_app_slug    text NOT NULL DEFAULT '',
    source_app_name    text NOT NULL DEFAULT '',
    target_app_id      text NOT NULL,
    target_app_slug    text NOT NULL DEFAULT '',
    target_visibility  text NOT NULL DEFAULT '',
    last_deployment_id text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    created_by_name    text NOT NULL DEFAULT '',
    UNIQUE (target_id, source_app_id)
);

-- A request names a deployment, not an app. The reviewer approves *those bytes*.
CREATE TABLE IF NOT EXISTS mc_requests (
    id                     text PRIMARY KEY,
    seq                    bigserial,
    status                 text NOT NULL,
    target_id              text NOT NULL REFERENCES mc_targets(id),
    source_app_id          text NOT NULL,
    source_app_slug        text NOT NULL DEFAULT '',
    source_app_name        text NOT NULL DEFAULT '',
    source_deployment_id   text NOT NULL,
    source_release_label   text NOT NULL DEFAULT '',
    requester_id           text NOT NULL,
    requester_name         text NOT NULL DEFAULT '',
    requester_email        text NOT NULL DEFAULT '',
    reason                 text NOT NULL DEFAULT '',
    -- The request-level note. Requester and approver both write it; it is not
    -- the decision reason, which belongs to the decision and stops being
    -- editable when the decision is made.
    note                   text NOT NULL DEFAULT '',
    -- What it will be called and who may see it, on the far side. Defaulted
    -- from the link where one exists and from the source app where none does.
    destination_slug       text NOT NULL DEFAULT '',
    destination_visibility text NOT NULL DEFAULT '',
    owner_group_id         text NOT NULL DEFAULT '',
    owner_group_name       text NOT NULL DEFAULT '',
    target_app_id          text NOT NULL DEFAULT '',
    notify_requester       boolean NOT NULL DEFAULT true,
    scheduled_for          timestamptz,
    via                    text NOT NULL DEFAULT '',
    decided_by_id          text NOT NULL DEFAULT '',
    decided_by_name        text NOT NULL DEFAULT '',
    decided_at             timestamptz,
    decision_reason        text NOT NULL DEFAULT '',
    -- Recorded permanently when the pinned release is not the one serving at
    -- execution: "was this the current code?" is what an auditor asks a year
    -- later, and neither install's own log can answer it.
    drift_note             text NOT NULL DEFAULT '',
    bundle_sha256          text NOT NULL DEFAULT '',
    bundle_bytes           bigint NOT NULL DEFAULT 0,
    target_deployment_id   text NOT NULL DEFAULT '',
    failure                text NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS mc_requests_source_idx ON mc_requests (source_app_id, created_at DESC);
CREATE INDEX IF NOT EXISTS mc_requests_status_idx ON mc_requests (status, created_at DESC);

-- People the requester added to a request. They read it, they watch it, and —
-- because they were trusted with this one — they may raise the next one for the
-- same source app. That last part is the whole point: a migration is usually a
-- team's, and a queue only its raiser can see is a queue somebody re-asks for
-- in a chat window.
CREATE TABLE IF NOT EXISTS mc_request_viewers (
    request_id    text NOT NULL REFERENCES mc_requests(id) ON DELETE CASCADE,
    user_id       text NOT NULL,
    user_name     text NOT NULL DEFAULT '',
    user_email    text NOT NULL DEFAULT '',
    added_by_name text NOT NULL DEFAULT '',
    added_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, user_id)
);

CREATE INDEX IF NOT EXISTS mc_request_viewers_user_idx ON mc_request_viewers (user_id);

-- One standing note per source app, outliving every request raised for it.
-- Where a request note says "why this release, this week", the app note says
-- "what a reviewer needs to know about this app every time" — the change
-- reference, the on-call rota, the thing that broke last quarter.
CREATE TABLE IF NOT EXISTS mc_app_notes (
    source_app_id   text PRIMARY KEY,
    body            text NOT NULL DEFAULT '',
    updated_by_id   text NOT NULL DEFAULT '',
    updated_by_name text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Supporting documents. Bytes in this database, served back only through the
-- console's own gate, and never with the uploader's content type — see
-- attachments.go, where the three rules that make that safe are stated.
--
-- Two scopes. A request attachment is evidence for one decision; an app
-- attachment is the standing paperwork every request for that app inherits, so
-- the change board stops re-attaching the same architecture note.
CREATE TABLE IF NOT EXISTS mc_attachments (
    id               text PRIMARY KEY,
    scope            text NOT NULL,               -- 'request' | 'app'
    request_id       text REFERENCES mc_requests(id) ON DELETE CASCADE,
    source_app_id    text NOT NULL DEFAULT '',
    filename         text NOT NULL,
    content_type     text NOT NULL DEFAULT '',
    size_bytes       bigint NOT NULL DEFAULT 0,
    sha256           text NOT NULL DEFAULT '',
    bytes            bytea NOT NULL,
    uploaded_by_id   text NOT NULL DEFAULT '',
    uploaded_by_name text NOT NULL DEFAULT '',
    uploaded_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS mc_attachments_request_idx ON mc_attachments (request_id);
CREATE INDEX IF NOT EXISTS mc_attachments_app_idx ON mc_attachments (source_app_id);

-- Zero or more checks per request. One of the three kinds has a producer today
-- — Toft's own dependency verdict for the exact release being promoted.
-- An absent check renders as absent, never as a pass.
CREATE TABLE IF NOT EXISTS mc_checks (
    id         text PRIMARY KEY,
    request_id text NOT NULL REFERENCES mc_requests(id) ON DELETE CASCADE,
    kind       text NOT NULL,               -- 'sast' | 'dast' | 'dependencies'
    tool       text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT '',
    verdict    text NOT NULL DEFAULT '',
    summary    text NOT NULL DEFAULT '',
    report     text NOT NULL DEFAULT '',
    ran_at     timestamptz
);

-- The ledger. Actions, not state changes. Append-only: nothing updates a row,
-- nothing prunes one, and the actor's *name* is copied rather than referenced
-- because a person who leaves Toft must not erase what they approved.
CREATE TABLE IF NOT EXISTS mc_events (
    id            bigserial PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    action        text NOT NULL,
    actor_id      text NOT NULL DEFAULT '',
    actor_name    text NOT NULL DEFAULT '',
    request_id    text,
    source_app_id text NOT NULL DEFAULT '',
    target_id     text NOT NULL DEFAULT '',
    detail        text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS mc_events_request_idx ON mc_events (request_id, id DESC);
CREATE INDEX IF NOT EXISTS mc_events_app_idx ON mc_events (source_app_id, id DESC);
