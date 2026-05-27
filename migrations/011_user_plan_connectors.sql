-- Connector metadata for plans sourced from personal data apps (health,
-- nutrition, fitness, etc.).

ALTER TABLE user_plans
    ADD COLUMN IF NOT EXISTS connectors JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS idx_user_plans_connectors_gin
    ON user_plans USING GIN (connectors);
