package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

var errPersonalDataStoreUnsupported = errors.New("personal data store not configured")

type PersonalDataBatchRequest struct {
	SourceSystem         string                      `json:"source_system"`
	SourceDevice         string                      `json:"source_device"`
	SourceApp            string                      `json:"source_app"`
	SyncStartedAt        string                      `json:"sync_started_at"`
	SyncCompletedAt      string                      `json:"sync_completed_at"`
	SchemaVersion        string                      `json:"schema_version"`
	NormalizationVersion string                      `json:"normalization_version"`
	Metadata             map[string]any              `json:"metadata_json"`
	Records              []PersonalDataRecordRequest `json:"records"`
}

type PersonalDataRecordRequest struct {
	SourceRecordType    string         `json:"source_record_type"`
	SourceRecordSubtype string         `json:"source_record_subtype"`
	SourceRecordID      string         `json:"source_record_id"`
	DedupeKey           string         `json:"dedupe_key"`
	StartTime           string         `json:"start_time"`
	EndTime             string         `json:"end_time"`
	ObservedAt          string         `json:"observed_at"`
	Value               *float64       `json:"value"`
	Unit                string         `json:"unit"`
	RawPayload          map[string]any `json:"raw_payload_json"`
	NormalizedPayload   map[string]any `json:"normalized_payload_json"`
	SourceMetadata      map[string]any `json:"source_metadata_json"`
	TrustLevel          string         `json:"trust_level"`
}

func (s *Service) IngestPersonalDataBatch(ctx context.Context, userID string, req PersonalDataBatchRequest) (store.SourceBatchIngestResult, error) {
	if s == nil || s.store == nil {
		return store.SourceBatchIngestResult{}, errPersonalDataStoreUnsupported
	}
	batchStore, ok := s.store.(store.SourceBatchStore)
	if !ok {
		return store.SourceBatchIngestResult{}, errPersonalDataStoreUnsupported
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return store.SourceBatchIngestResult{}, fmt.Errorf("user id is required")
	}

	sourceSystem := strings.TrimSpace(req.SourceSystem)
	if sourceSystem == "" {
		return store.SourceBatchIngestResult{}, fmt.Errorf("source_system is required")
	}
	if !isAllowedSourceSystem(sourceSystem) {
		return store.SourceBatchIngestResult{}, fmt.Errorf("unsupported source_system %q", sourceSystem)
	}
	if len(req.Records) == 0 {
		return store.SourceBatchIngestResult{}, fmt.Errorf("records are required")
	}

	batchID := "batch-" + newID()
	batch := &store.SourceBatch{
		ID:                   batchID,
		UserID:               userID,
		SourceSystem:         sourceSystem,
		SourceDevice:         strings.TrimSpace(req.SourceDevice),
		SourceApp:            strings.TrimSpace(req.SourceApp),
		Status:               "accepted",
		SchemaVersion:        strings.TrimSpace(req.SchemaVersion),
		NormalizationVersion: strings.TrimSpace(req.NormalizationVersion),
		RecordCountReceived:  len(req.Records),
		Metadata:             req.Metadata,
	}
	if startedAt, err := parseOptionalTime(req.SyncStartedAt); err != nil {
		return store.SourceBatchIngestResult{}, fmt.Errorf("sync_started_at: %w", err)
	} else {
		batch.SyncStartedAt = startedAt
	}
	if completedAt, err := parseOptionalTime(req.SyncCompletedAt); err != nil {
		return store.SourceBatchIngestResult{}, fmt.Errorf("sync_completed_at: %w", err)
	} else {
		batch.SyncCompletedAt = completedAt
	}

	records := make([]store.SourceRecord, 0, len(req.Records))
	rejected := make([]store.RejectedSourceRecord, 0)
	for _, rawRecord := range req.Records {
		record, fieldErrors := normalizeSourceRecord(userID, batchID, sourceSystem, batch.SchemaVersion, batch.NormalizationVersion, rawRecord)
		if len(fieldErrors) > 0 {
			rejected = append(rejected, store.RejectedSourceRecord{
				BatchID:         batchID,
				UserID:          userID,
				SourceSystem:    sourceSystem,
				RawPayload:      rawRecordPayload(rawRecord),
				RejectionReason: "validation_failed",
				FieldErrors:     fieldErrors,
			})
			continue
		}
		records = append(records, record)
	}
	batch.RecordCountRejected = len(rejected)
	batch.Status = batchStatus(len(records), len(rejected))
	if len(records) == 0 && len(rejected) > 0 {
		batch.ErrorSummary = "all records failed validation"
	}

	result, err := batchStore.IngestSourceBatch(ctx, batch, records, rejected)
	if err != nil {
		return store.SourceBatchIngestResult{}, err
	}
	if len(records) > 0 {
		if err := s.MapPersonalDataBatch(ctx, userID, result.BatchID); err != nil {
			result.ProcessingStatus = "failed"
		} else {
			result.ProcessingStatus = "processed"
		}
	}
	return result, nil
}

func (s *Service) MapPersonalDataBatch(ctx context.Context, userID, batchID string) error {
	mappingStore, ok := s.store.(store.PersonalDataMappingStore)
	if !ok {
		return nil
	}
	records, err := mappingStore.ListSourceRecordsForBatch(ctx, userID, batchID)
	if err != nil {
		return err
	}
	contributions, rollups := mapSourceRecordsToDailyProgress(userID, records)
	if err := mappingStore.UpsertProgressContributions(ctx, contributions); err != nil {
		return err
	}
	for _, rollup := range rollups {
		if err := mappingStore.UpsertDailyRollup(ctx, rollup); err != nil {
			return err
		}
	}
	return nil
}

func mapSourceRecordsToDailyProgress(userID string, records []store.SourceRecord) ([]store.ProgressContribution, []*store.DailyRollup) {
	var contributions []store.ProgressContribution
	rollupBuilders := map[string]*dailyRollupBuilder{}
	for _, record := range records {
		if record.Value == nil || record.SourceSystem != "apple_healthkit" {
			continue
		}
		bucket, ok := healthDailyBucket(record)
		if !ok {
			continue
		}
		date := sourceRecordDate(record)
		if date == "" {
			continue
		}
		targetID := date + ":" + bucket
		confidence := sourceRecordConfidence(record)
		contributions = append(contributions, store.ProgressContribution{
			ID:                 deterministicID("pc", userID, record.ID, targetID, record.SourceRecordSubtype),
			UserID:             userID,
			SourceRecordID:     record.ID,
			TargetType:         "daily_bucket",
			TargetID:           targetID,
			ContributionType:   "partial_progress",
			Amount:             record.Value,
			Unit:               record.Unit,
			Confidence:         &confidence,
			MappingRule:        "apple_healthkit_daily_bucket",
			MappingRuleVersion: "v1",
			MapperVersion:      "personal-data-mapper.v1",
		})

		builder := rollupBuilders[date]
		if builder == nil {
			builder = newDailyRollupBuilder(userID, date)
			rollupBuilders[date] = builder
		}
		builder.add(record, bucket, targetID)
	}

	dates := make([]string, 0, len(rollupBuilders))
	for date := range rollupBuilders {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	rollups := make([]*store.DailyRollup, 0, len(dates))
	for _, date := range dates {
		rollups = append(rollups, rollupBuilders[date].rollup())
	}
	return contributions, rollups
}

type dailyRollupBuilder struct {
	userID        string
	date          string
	sourceSystems map[string]bool
	buckets       map[string][]map[string]any
	totals        map[string]map[string]any
	recordCount   int
}

func newDailyRollupBuilder(userID, date string) *dailyRollupBuilder {
	return &dailyRollupBuilder{
		userID:        userID,
		date:          date,
		sourceSystems: map[string]bool{},
		buckets:       map[string][]map[string]any{},
		totals:        map[string]map[string]any{},
	}
}

func (b *dailyRollupBuilder) add(record store.SourceRecord, bucket, targetID string) {
	b.sourceSystems[record.SourceSystem] = true
	b.recordCount++
	metric := record.SourceRecordSubtype
	if metric == "" {
		metric = record.SourceRecordType
	}
	value := 0.0
	if record.Value != nil {
		value = *record.Value
	}
	entry := map[string]any{
		"source_record_id": record.ID,
		"metric":           metric,
		"value":            value,
		"unit":             record.Unit,
		"target_id":        targetID,
	}
	b.buckets[bucket] = append(b.buckets[bucket], entry)
	if existing, ok := b.totals[metric]; ok {
		existing["value"] = existing["value"].(float64) + value
	} else {
		b.totals[metric] = map[string]any{"value": value, "unit": record.Unit, "bucket": bucket}
	}
}

func (b *dailyRollupBuilder) rollup() *store.DailyRollup {
	dateValue, _ := time.Parse("2006-01-02", b.date)
	sourceSystems := make([]string, 0, len(b.sourceSystems))
	for sourceSystem := range b.sourceSystems {
		sourceSystems = append(sourceSystems, sourceSystem)
	}
	sort.Strings(sourceSystems)
	return &store.DailyRollup{
		ID:                    deterministicID("rollup", b.userID, b.date, "daily-rollup.v1"),
		UserID:                b.userID,
		Date:                  dateValue,
		SourceSystemsIncluded: sourceSystems,
		StructuredRollup: map[string]any{
			"date":         b.date,
			"buckets":      b.buckets,
			"totals":       b.totals,
			"record_count": b.recordCount,
		},
		SummaryText:              fmt.Sprintf("Mapped %d Apple Health records for %s.", b.recordCount, b.date),
		UnmappedRecordsSummary:   map[string]any{"count": 0},
		Conflicts:                []map[string]any{},
		RollupVersion:            "daily-rollup.v1",
		SummaryGenerationVersion: "deterministic.v1",
	}
}

func healthDailyBucket(record store.SourceRecord) (string, bool) {
	subtype := strings.ToLower(strings.TrimSpace(record.SourceRecordSubtype))
	switch record.SourceRecordType {
	case "health.workout":
		return "general_exercise", true
	case "health.activity":
		if strings.Contains(subtype, "exercise") || strings.Contains(subtype, "workout") || strings.Contains(subtype, "distance") || strings.Contains(subtype, "energy") {
			return "general_exercise", true
		}
		return "activity", true
	case "health.nutrition":
		return "nutrition", true
	case "health.sleep":
		return "sleep", true
	case "health.body_metric":
		return "body_composition", true
	default:
		return "", false
	}
}

func sourceRecordDate(record store.SourceRecord) string {
	when := record.StartTime
	if when == nil {
		when = record.ObservedAt
	}
	if when == nil {
		when = record.EndTime
	}
	if when == nil {
		return ""
	}
	loc := when.Location()
	if timezone, ok := record.NormalizedPayload["timezone"].(string); ok && strings.TrimSpace(timezone) != "" {
		if loaded, err := time.LoadLocation(strings.TrimSpace(timezone)); err == nil {
			loc = loaded
		}
	}
	return when.In(loc).Format("2006-01-02")
}

func sourceRecordConfidence(record store.SourceRecord) float64 {
	switch strings.TrimSpace(record.TrustLevel) {
	case "device_measured":
		return 0.95
	case "app_reported":
		return 0.85
	case "manual":
		return 0.75
	case "estimated":
		return 0.6
	default:
		return 0.7
	}
}

func deterministicID(prefix string, parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return prefix + "-" + hex.EncodeToString(h.Sum(nil))
}

func normalizeSourceRecord(userID, batchID, sourceSystem, schemaVersion, normalizationVersion string, raw PersonalDataRecordRequest) (store.SourceRecord, map[string]any) {
	fieldErrors := map[string]any{}
	recordType := strings.TrimSpace(raw.SourceRecordType)
	if recordType == "" {
		fieldErrors["source_record_type"] = "required"
	} else if !isAllowedRecordType(sourceSystem, recordType) {
		fieldErrors["source_record_type"] = "unsupported for source_system"
	}
	sourceRecordID := strings.TrimSpace(raw.SourceRecordID)
	dedupeKey := strings.TrimSpace(raw.DedupeKey)
	if sourceRecordID == "" && dedupeKey == "" {
		fieldErrors["source_record_id"] = "source_record_id or dedupe_key is required"
	}
	if sourceRecordID == "" {
		sourceRecordID = dedupeKey
	}
	if dedupeKey == "" {
		dedupeKey = sourceSystem + ":" + recordType + ":" + sourceRecordID
	}

	startTime, err := parseOptionalTime(raw.StartTime)
	if err != nil {
		fieldErrors["start_time"] = err.Error()
	}
	endTime, err := parseOptionalTime(raw.EndTime)
	if err != nil {
		fieldErrors["end_time"] = err.Error()
	}
	observedAt, err := parseOptionalTime(raw.ObservedAt)
	if err != nil {
		fieldErrors["observed_at"] = err.Error()
	}
	if startTime != nil && endTime != nil && endTime.Before(*startTime) {
		fieldErrors["end_time"] = "must be after start_time"
	}
	if raw.Value != nil && strings.TrimSpace(raw.Unit) == "" {
		fieldErrors["unit"] = "required when value is present"
	}

	if len(fieldErrors) > 0 {
		return store.SourceRecord{}, fieldErrors
	}
	return store.SourceRecord{
		ID:                   "src-" + newID(),
		UserID:               userID,
		BatchID:              batchID,
		SourceSystem:         sourceSystem,
		SourceRecordType:     recordType,
		SourceRecordSubtype:  strings.TrimSpace(raw.SourceRecordSubtype),
		SourceRecordID:       sourceRecordID,
		DedupeKey:            dedupeKey,
		StartTime:            startTime,
		EndTime:              endTime,
		ObservedAt:           observedAt,
		Value:                raw.Value,
		Unit:                 strings.TrimSpace(raw.Unit),
		RawPayload:           raw.RawPayload,
		NormalizedPayload:    raw.NormalizedPayload,
		SourceMetadata:       raw.SourceMetadata,
		TrustLevel:           strings.TrimSpace(raw.TrustLevel),
		SchemaVersion:        schemaVersion,
		NormalizationVersion: normalizationVersion,
	}, nil
}

func parseOptionalTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("must be RFC3339 timestamp")
	}
	return &parsed, nil
}

func rawRecordPayload(record PersonalDataRecordRequest) map[string]any {
	payload := map[string]any{
		"source_record_type":    record.SourceRecordType,
		"source_record_subtype": record.SourceRecordSubtype,
		"source_record_id":      record.SourceRecordID,
		"dedupe_key":            record.DedupeKey,
		"start_time":            record.StartTime,
		"end_time":              record.EndTime,
		"observed_at":           record.ObservedAt,
		"unit":                  record.Unit,
		"trust_level":           record.TrustLevel,
	}
	if record.Value != nil {
		payload["value"] = *record.Value
	}
	if record.RawPayload != nil {
		payload["raw_payload_json"] = record.RawPayload
	}
	if record.NormalizedPayload != nil {
		payload["normalized_payload_json"] = record.NormalizedPayload
	}
	if record.SourceMetadata != nil {
		payload["source_metadata_json"] = record.SourceMetadata
	}
	return payload
}

func batchStatus(accepted, rejected int) string {
	switch {
	case accepted > 0 && rejected > 0:
		return "partially_accepted"
	case accepted > 0:
		return "accepted"
	default:
		return "rejected"
	}
}

func isAllowedSourceSystem(sourceSystem string) bool {
	_, ok := sourceRecordTypePrefixes()[sourceSystem]
	return ok
}

func isAllowedRecordType(sourceSystem, recordType string) bool {
	prefixes, ok := sourceRecordTypePrefixes()[sourceSystem]
	if !ok {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(recordType, prefix) {
			return true
		}
	}
	return false
}

func sourceRecordTypePrefixes() map[string][]string {
	return map[string][]string{
		"apple_healthkit": {"health."},
		"calendar":        {"calendar."},
		"github":          {"github."},
		"home_lab":        {"home_lab."},
		"manual_entry":    {"health.", "nutrition.", "task.", "project.", "finance.", "location."},
		"training_plan":   {"training."},
		"nutrition_log":   {"health.nutrition", "nutrition."},
		"task_activity":   {"task.", "project."},
		"location":        {"location."},
		"finance":         {"finance."},
	}
}
