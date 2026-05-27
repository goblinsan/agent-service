-- Hierarchical plan support: vision/target + milestones/tasks + roll-up progress.

ALTER TABLE user_plans
    ADD COLUMN IF NOT EXISTS vision TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS milestones JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS progress JSONB NOT NULL DEFAULT '{}'::jsonb;
