-- Preserve the local timezone intended by wall-clock recurring schedules.

ALTER TABLE scheduled_jobs
    ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_user_kind
    ON scheduled_jobs (user_id, kind, updated_at DESC);
