-- Richer content sections for durable plans so imports can preserve planning
-- context beyond milestones/tasks.

ALTER TABLE user_plans
    ADD COLUMN IF NOT EXISTS objectives JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS principles JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS tracked_metrics JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS baseline_facts JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS success_criteria JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS cadence JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS supporting_sections JSONB NOT NULL DEFAULT '[]'::jsonb;
