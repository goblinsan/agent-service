ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS prompt_tokens      INTEGER,
    ADD COLUMN IF NOT EXISTS completion_tokens  INTEGER,
    ADD COLUMN IF NOT EXISTS total_tokens       INTEGER;
