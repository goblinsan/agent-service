package store

import (
	"context"
	"time"
)

type SourceBatch struct {
	ID                   string         `json:"id"`
	UserID               string         `json:"user_id"`
	SourceSystem         string         `json:"source_system"`
	SourceDevice         string         `json:"source_device,omitempty"`
	SourceApp            string         `json:"source_app,omitempty"`
	SyncStartedAt        *time.Time     `json:"sync_started_at,omitempty"`
	SyncCompletedAt      *time.Time     `json:"sync_completed_at,omitempty"`
	Status               string         `json:"status"`
	SchemaVersion        string         `json:"schema_version,omitempty"`
	NormalizationVersion string         `json:"normalization_version,omitempty"`
	RecordCountReceived  int            `json:"record_count_received"`
	RecordCountInserted  int            `json:"record_count_inserted"`
	RecordCountUpdated   int            `json:"record_count_updated"`
	RecordCountRejected  int            `json:"record_count_rejected"`
	ErrorSummary         string         `json:"error_summary,omitempty"`
	Metadata             map[string]any `json:"metadata_json,omitempty"`
	ReceivedAt           time.Time      `json:"received_at"`
}

type SourceRecord struct {
	ID                   string         `json:"id"`
	UserID               string         `json:"user_id"`
	BatchID              string         `json:"batch_id"`
	SourceSystem         string         `json:"source_system"`
	SourceRecordType     string         `json:"source_record_type"`
	SourceRecordSubtype  string         `json:"source_record_subtype,omitempty"`
	SourceRecordID       string         `json:"source_record_id"`
	DedupeKey            string         `json:"dedupe_key"`
	StartTime            *time.Time     `json:"start_time,omitempty"`
	EndTime              *time.Time     `json:"end_time,omitempty"`
	ObservedAt           *time.Time     `json:"observed_at,omitempty"`
	Value                *float64       `json:"value,omitempty"`
	Unit                 string         `json:"unit,omitempty"`
	RawPayload           map[string]any `json:"raw_payload_json,omitempty"`
	NormalizedPayload    map[string]any `json:"normalized_payload_json,omitempty"`
	SourceMetadata       map[string]any `json:"source_metadata_json,omitempty"`
	TrustLevel           string         `json:"trust_level,omitempty"`
	SchemaVersion        string         `json:"schema_version,omitempty"`
	NormalizationVersion string         `json:"normalization_version,omitempty"`
}

type RejectedSourceRecord struct {
	BatchID         string         `json:"batch_id"`
	UserID          string         `json:"user_id"`
	SourceSystem    string         `json:"source_system"`
	RawPayload      map[string]any `json:"raw_payload_json,omitempty"`
	RejectionReason string         `json:"rejection_reason"`
	FieldErrors     map[string]any `json:"field_errors_json,omitempty"`
}

type SourceBatchIngestResult struct {
	BatchID          string `json:"batch_id"`
	Status           string `json:"status"`
	Received         int    `json:"received"`
	Inserted         int    `json:"inserted"`
	Updated          int    `json:"updated"`
	Rejected         int    `json:"rejected"`
	ProcessingStatus string `json:"processing_status"`
}

type ProgressContribution struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"user_id"`
	SourceRecordID     string    `json:"source_record_id,omitempty"`
	TargetType         string    `json:"target_type"`
	TargetID           string    `json:"target_id,omitempty"`
	ContributionType   string    `json:"contribution_type"`
	Amount             *float64  `json:"amount,omitempty"`
	Unit               string    `json:"unit,omitempty"`
	Confidence         *float64  `json:"confidence,omitempty"`
	MappingRule        string    `json:"mapping_rule,omitempty"`
	MappingRuleVersion string    `json:"mapping_rule_version,omitempty"`
	MapperVersion      string    `json:"mapper_version,omitempty"`
	IsManualOverride   bool      `json:"is_manual_override"`
	ManualOverrideID   string    `json:"manual_override_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type DailyRollup struct {
	ID                       string           `json:"id"`
	UserID                   string           `json:"user_id"`
	Date                     time.Time        `json:"date"`
	SourceSystemsIncluded    []string         `json:"source_systems_included"`
	StructuredRollup         map[string]any   `json:"structured_rollup_json"`
	SummaryText              string           `json:"summary_text,omitempty"`
	LookaheadText            string           `json:"lookahead_text,omitempty"`
	UnmappedRecordsSummary   map[string]any   `json:"unmapped_records_summary_json,omitempty"`
	Conflicts                []map[string]any `json:"conflicts_json,omitempty"`
	RollupVersion            string           `json:"rollup_version,omitempty"`
	SummaryGenerationVersion string           `json:"summary_generation_version,omitempty"`
	CreatedAt                time.Time        `json:"created_at"`
	UpdatedAt                time.Time        `json:"updated_at"`
}

type SourceBatchStore interface {
	IngestSourceBatch(ctx context.Context, batch *SourceBatch, records []SourceRecord, rejected []RejectedSourceRecord) (SourceBatchIngestResult, error)
}

type PersonalDataMappingStore interface {
	ListSourceRecordsForBatch(ctx context.Context, userID, batchID string) ([]SourceRecord, error)
	UpsertProgressContributions(ctx context.Context, contributions []ProgressContribution) error
	UpsertDailyRollup(ctx context.Context, rollup *DailyRollup) error
}

type DailyRollupStore interface {
	ListDailyRollups(ctx context.Context, userID string, startDate, endDate time.Time) ([]DailyRollup, error)
}

type UnmappedSourceRecordStore interface {
	ListUnmappedSourceRecords(ctx context.Context, userID string, startDate, endDate time.Time, limit int) ([]SourceRecord, error)
}
