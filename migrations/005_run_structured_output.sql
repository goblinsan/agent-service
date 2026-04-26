ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS structured_output JSONB;
