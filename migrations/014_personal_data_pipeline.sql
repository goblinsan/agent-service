-- 014_personal_data_pipeline.sql
-- Generalized personal data ingestion foundation.

CREATE TABLE IF NOT EXISTS source_batches (
    id                       TEXT PRIMARY KEY,
    user_id                  TEXT NOT NULL,
    source_system            TEXT NOT NULL,
    source_device            TEXT,
    source_app               TEXT,
    sync_started_at          TIMESTAMPTZ,
    sync_completed_at        TIMESTAMPTZ,
    received_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status                   TEXT NOT NULL,
    schema_version           TEXT,
    normalization_version    TEXT,
    record_count_received    INTEGER NOT NULL DEFAULT 0,
    record_count_inserted    INTEGER NOT NULL DEFAULT 0,
    record_count_updated     INTEGER NOT NULL DEFAULT 0,
    record_count_rejected    INTEGER NOT NULL DEFAULT 0,
    error_summary            TEXT,
    metadata_json            JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_source_batches_user_received
    ON source_batches (user_id, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_source_batches_source_status
    ON source_batches (source_system, status);

CREATE TABLE IF NOT EXISTS source_records (
    id                       TEXT PRIMARY KEY,
    user_id                  TEXT NOT NULL,
    batch_id                 TEXT NOT NULL REFERENCES source_batches(id) ON DELETE CASCADE,
    source_system            TEXT NOT NULL,
    source_record_type       TEXT NOT NULL,
    source_record_subtype    TEXT,
    source_record_id         TEXT NOT NULL,
    dedupe_key               TEXT NOT NULL,
    start_time               TIMESTAMPTZ,
    end_time                 TIMESTAMPTZ,
    observed_at              TIMESTAMPTZ,
    value                    DOUBLE PRECISION,
    unit                     TEXT,
    raw_payload_json         JSONB NOT NULL DEFAULT '{}'::jsonb,
    normalized_payload_json  JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_metadata_json     JSONB NOT NULL DEFAULT '{}'::jsonb,
    trust_level              TEXT,
    schema_version           TEXT,
    normalization_version    TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, source_system, dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_source_records_user_time
    ON source_records (user_id, (COALESCE(start_time, observed_at, created_at)) DESC);

CREATE INDEX IF NOT EXISTS idx_source_records_user_type
    ON source_records (user_id, source_record_type, source_record_subtype);

CREATE INDEX IF NOT EXISTS idx_source_records_batch
    ON source_records (batch_id);

CREATE TABLE IF NOT EXISTS rejected_source_records (
    id                   BIGSERIAL PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES source_batches(id) ON DELETE CASCADE,
    user_id              TEXT NOT NULL,
    source_system        TEXT NOT NULL,
    raw_payload_json     JSONB NOT NULL DEFAULT '{}'::jsonb,
    rejection_reason     TEXT NOT NULL,
    field_errors_json    JSONB NOT NULL DEFAULT '{}'::jsonb,
    received_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rejected_source_records_batch
    ON rejected_source_records (batch_id);

CREATE INDEX IF NOT EXISTS idx_rejected_source_records_user_received
    ON rejected_source_records (user_id, received_at DESC);

CREATE TABLE IF NOT EXISTS progress_contributions (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL,
    source_record_id      TEXT REFERENCES source_records(id) ON DELETE SET NULL,
    target_type           TEXT NOT NULL,
    target_id             TEXT,
    contribution_type     TEXT NOT NULL,
    amount                DOUBLE PRECISION,
    unit                  TEXT,
    confidence            DOUBLE PRECISION,
    mapping_rule          TEXT,
    mapping_rule_version  TEXT,
    mapper_version        TEXT,
    is_manual_override    BOOLEAN NOT NULL DEFAULT FALSE,
    manual_override_id    TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_progress_contributions_user_target
    ON progress_contributions (user_id, target_type, target_id);

CREATE INDEX IF NOT EXISTS idx_progress_contributions_source_record
    ON progress_contributions (source_record_id);

CREATE TABLE IF NOT EXISTS manual_mapping_overrides (
    id                         TEXT PRIMARY KEY,
    user_id                    TEXT NOT NULL,
    source_record_id           TEXT REFERENCES source_records(id) ON DELETE CASCADE,
    original_contribution_id   TEXT REFERENCES progress_contributions(id) ON DELETE SET NULL,
    override_action            TEXT NOT NULL,
    target_type                TEXT,
    target_id                  TEXT,
    amount                     DOUBLE PRECISION,
    unit                       TEXT,
    reason                     TEXT,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_manual_mapping_overrides_user_created
    ON manual_mapping_overrides (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_manual_mapping_overrides_source_record
    ON manual_mapping_overrides (source_record_id);

CREATE TABLE IF NOT EXISTS daily_rollups (
    id                              TEXT PRIMARY KEY,
    user_id                         TEXT NOT NULL,
    date                            DATE NOT NULL,
    source_systems_included         JSONB NOT NULL DEFAULT '[]'::jsonb,
    structured_rollup_json          JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary_text                    TEXT,
    lookahead_text                  TEXT,
    unmapped_records_summary_json   JSONB NOT NULL DEFAULT '{}'::jsonb,
    conflicts_json                  JSONB NOT NULL DEFAULT '[]'::jsonb,
    rollup_version                  TEXT,
    summary_generation_version      TEXT,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, date, rollup_version)
);

CREATE INDEX IF NOT EXISTS idx_daily_rollups_user_date
    ON daily_rollups (user_id, date DESC);
