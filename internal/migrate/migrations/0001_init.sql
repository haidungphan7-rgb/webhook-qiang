-- Webhook replay platform - initial schema.
--
-- Conventions:
--   * timestamps are timestamptz (UTC) everywhere; the UI renders in local time;
--   * bodies are bytea - we store exactly the bytes that arrived;
--   * every list query is covered by an index that matches its ORDER BY.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- inboxes -------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS inbox (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_key            text        NOT NULL DEFAULT 'default',
    name                 text        NOT NULL,
    token                text        NOT NULL UNIQUE,
    enabled              boolean     NOT NULL DEFAULT true,
    -- custom response returned to the webhook sender
    response_code        int         NOT NULL DEFAULT 200,
    response_headers     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    response_body        bytea       NOT NULL DEFAULT ''::bytea,
    response_delay_ms    int         NOT NULL DEFAULT 0,
    -- optional HMAC signature verification
    signing_secret_enc   bytea,
    signature_header     text        NOT NULL DEFAULT 'X-Signature',
    signature_scheme     text        NOT NULL DEFAULT 'hmac-sha256-hex',
    require_signature    boolean     NOT NULL DEFAULT false,
    -- retention (per inbox, defaults come from the app config on create)
    retention_max_events int         NOT NULL DEFAULT 500,
    retention_max_days   int         NOT NULL DEFAULT 30,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS inbox_owner_created_idx ON inbox (owner_key, created_at DESC);
CREATE INDEX IF NOT EXISTS inbox_created_idx       ON inbox (created_at DESC);

-- captured events -----------------------------------------------------------
CREATE TABLE IF NOT EXISTS event (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inbox_id        uuid        NOT NULL REFERENCES inbox (id) ON DELETE CASCADE,
    method          text        NOT NULL,
    path            text        NOT NULL DEFAULT '',
    query           text        NOT NULL DEFAULT '',
    content_type    text        NOT NULL DEFAULT '',
    headers         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    body            bytea       NOT NULL,
    body_size       int         NOT NULL,
    client_ip       text        NOT NULL DEFAULT '',
    signature_valid boolean,          -- NULL = signature not verified
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS event_inbox_created_idx ON event (inbox_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_inbox_ct_idx      ON event (inbox_id, content_type);
CREATE INDEX IF NOT EXISTS event_created_idx       ON event (created_at DESC);

-- replay attempts -----------------------------------------------------------
CREATE TABLE IF NOT EXISTS replay_attempt (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id          uuid        NOT NULL REFERENCES event (id) ON DELETE CASCADE,
    inbox_id          uuid        NOT NULL REFERENCES inbox (id) ON DELETE CASCADE,
    attempt_no        int         NOT NULL DEFAULT 1,
    retry_of          uuid        REFERENCES replay_attempt (id) ON DELETE SET NULL,
    target_url        text        NOT NULL,
    edited_input      jsonb,
    sign_applied      boolean     NOT NULL DEFAULT false,
    started_at        timestamptz NOT NULL,
    finished_at       timestamptz,
    duration_ms       int         NOT NULL DEFAULT 0,
    status_code       int,
    response_preview  text        NOT NULL DEFAULT '',
    preview_truncated boolean     NOT NULL DEFAULT false,
    error             text,
    outcome           text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS replay_event_idx ON replay_attempt (event_id, created_at DESC);
CREATE INDEX IF NOT EXISTS replay_inbox_idx ON replay_attempt (inbox_id, created_at DESC);

-- migration bookkeeping -----------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
