-- Scheduled jobs, notifications inbox, and push device tokens.

CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT 'prompt',
    prompt       TEXT NOT NULL DEFAULT '',
    thread_id    TEXT,
    agent_id     TEXT,
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    run_at       TIMESTAMPTZ NOT NULL,
    recurrence   TEXT,
    status       TEXT NOT NULL DEFAULT 'pending',
    locked_until TIMESTAMPTZ,
    last_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_due
    ON scheduled_jobs (status, run_at, locked_until);

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_user
    ON scheduled_jobs (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS notifications (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    kind          TEXT NOT NULL,
    title         TEXT NOT NULL,
    body          TEXT,
    thread_id     TEXT,
    source_run_id TEXT,
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    read_at       TIMESTAMPTZ,
    dismissed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_time
    ON notifications (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread
    ON notifications (user_id, read_at, dismissed_at);

CREATE TABLE IF NOT EXISTS device_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    platform     TEXT NOT NULL,
    token        TEXT NOT NULL,
    app_version  TEXT,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, platform, token)
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_user_platform
    ON device_tokens (user_id, platform, last_seen_at DESC);
