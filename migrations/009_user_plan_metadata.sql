-- Typed metadata for user plans/goals so domain-specific assistants can
-- distinguish health, work, finance, and other plan classes.

ALTER TABLE user_plans
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS data_sources JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS review_cadence TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metrics JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_user_plans_user_category
    ON user_plans (user_id, category, updated_at DESC);
