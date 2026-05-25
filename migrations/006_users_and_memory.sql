-- 006_users_and_memory.sql
-- Per-user identity, event log, durable memories, and plans.
-- Driven by the pivot to agent-service-as-sole-LLM-provider (see homeops
-- handoff doc 2026-05-25). user_id columns intentionally have NO FK to
-- users(id) so existing run rows with unknown users don't block migration;
-- application calls EnsureUser at request time.

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    display_name  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_events (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL,
    session_id  TEXT,
    kind        TEXT NOT NULL,
    source      TEXT NOT NULL,
    summary     TEXT NOT NULL,
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_events_user_time
    ON user_events (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_events_user_kind
    ON user_events (user_id, kind);

CREATE TABLE IF NOT EXISTS user_memories (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    confidence  REAL NOT NULL DEFAULT 1.0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, key)
);

CREATE TABLE IF NOT EXISTS user_plans (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    title       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',
    summary     TEXT,
    steps       JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_plans_user_status
    ON user_plans (user_id, status);

INSERT INTO users (id, display_name)
VALUES ('jimmothy', 'James')
ON CONFLICT (id) DO NOTHING;
