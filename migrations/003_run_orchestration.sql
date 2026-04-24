-- Make session_id nullable so automation runs are not forced to reference a session.
ALTER TABLE runs ALTER COLUMN session_id DROP NOT NULL;

-- Orchestration context columns.
ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS source            TEXT NOT NULL DEFAULT 'chat',
    ADD COLUMN IF NOT EXISTS model_backend     TEXT,
    ADD COLUMN IF NOT EXISTS tool_calls        TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS approval_records  TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS paused_state      TEXT,
    ADD COLUMN IF NOT EXISTS request_id        TEXT,
    ADD COLUMN IF NOT EXISTS thread_id         TEXT,
    ADD COLUMN IF NOT EXISTS user_id           TEXT,
    ADD COLUMN IF NOT EXISTS agent_id          TEXT,
    ADD COLUMN IF NOT EXISTS workflow_id       TEXT,
    ADD COLUMN IF NOT EXISTS job_type          TEXT;
