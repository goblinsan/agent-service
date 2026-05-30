package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) IngestSourceBatch(ctx context.Context, batch *SourceBatch, records []SourceRecord, rejected []RejectedSourceRecord) (SourceBatchIngestResult, error) {
	if batch == nil || batch.ID == "" || batch.UserID == "" || batch.SourceSystem == "" {
		return SourceBatchIngestResult{}, errors.New("source batch requires id, user_id, and source_system")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceBatchIngestResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	metadataRaw, err := marshalMap(batch.Metadata)
	if err != nil {
		return SourceBatchIngestResult{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO source_batches (
			id, user_id, source_system, source_device, source_app, sync_started_at, sync_completed_at,
			status, schema_version, normalization_version, record_count_received, record_count_rejected,
			error_summary, metadata_json
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			record_count_received = EXCLUDED.record_count_received,
			record_count_rejected = EXCLUDED.record_count_rejected,
			error_summary = EXCLUDED.error_summary,
			metadata_json = EXCLUDED.metadata_json`,
		batch.ID,
		batch.UserID,
		batch.SourceSystem,
		nullableString(batch.SourceDevice),
		nullableString(batch.SourceApp),
		nullableTimePtr(batch.SyncStartedAt),
		nullableTimePtr(batch.SyncCompletedAt),
		batch.Status,
		nullableString(batch.SchemaVersion),
		nullableString(batch.NormalizationVersion),
		batch.RecordCountReceived,
		len(rejected),
		nullableString(batch.ErrorSummary),
		string(metadataRaw),
	); err != nil {
		return SourceBatchIngestResult{}, err
	}

	inserted := 0
	updated := 0
	for _, record := range records {
		rawPayload, err := marshalMap(record.RawPayload)
		if err != nil {
			return SourceBatchIngestResult{}, err
		}
		normalizedPayload, err := marshalMap(record.NormalizedPayload)
		if err != nil {
			return SourceBatchIngestResult{}, err
		}
		sourceMetadata, err := marshalMap(record.SourceMetadata)
		if err != nil {
			return SourceBatchIngestResult{}, err
		}
		var insertedRow bool
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO source_records (
				id, user_id, batch_id, source_system, source_record_type, source_record_subtype,
				source_record_id, dedupe_key, start_time, end_time, observed_at, value, unit,
				raw_payload_json, normalized_payload_json, source_metadata_json, trust_level,
				schema_version, normalization_version
			 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
			 ON CONFLICT (user_id, source_system, dedupe_key) DO UPDATE SET
				batch_id = EXCLUDED.batch_id,
				source_record_type = EXCLUDED.source_record_type,
				source_record_subtype = EXCLUDED.source_record_subtype,
				source_record_id = EXCLUDED.source_record_id,
				start_time = EXCLUDED.start_time,
				end_time = EXCLUDED.end_time,
				observed_at = EXCLUDED.observed_at,
				value = EXCLUDED.value,
				unit = EXCLUDED.unit,
				raw_payload_json = EXCLUDED.raw_payload_json,
				normalized_payload_json = EXCLUDED.normalized_payload_json,
				source_metadata_json = EXCLUDED.source_metadata_json,
				trust_level = EXCLUDED.trust_level,
				schema_version = EXCLUDED.schema_version,
				normalization_version = EXCLUDED.normalization_version,
				updated_at = NOW()
			 RETURNING (xmax = 0)`,
			record.ID,
			record.UserID,
			record.BatchID,
			record.SourceSystem,
			record.SourceRecordType,
			nullableString(record.SourceRecordSubtype),
			record.SourceRecordID,
			record.DedupeKey,
			nullableTimePtr(record.StartTime),
			nullableTimePtr(record.EndTime),
			nullableTimePtr(record.ObservedAt),
			nullableFloat(record.Value),
			nullableString(record.Unit),
			string(rawPayload),
			string(normalizedPayload),
			string(sourceMetadata),
			nullableString(record.TrustLevel),
			nullableString(record.SchemaVersion),
			nullableString(record.NormalizationVersion),
		).Scan(&insertedRow); err != nil {
			return SourceBatchIngestResult{}, err
		}
		if insertedRow {
			inserted++
		} else {
			updated++
		}
	}

	for _, rejectedRecord := range rejected {
		rawPayload, err := marshalMap(rejectedRecord.RawPayload)
		if err != nil {
			return SourceBatchIngestResult{}, err
		}
		fieldErrors, err := marshalMap(rejectedRecord.FieldErrors)
		if err != nil {
			return SourceBatchIngestResult{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rejected_source_records (
				batch_id, user_id, source_system, raw_payload_json, rejection_reason, field_errors_json
			 ) VALUES ($1, $2, $3, $4, $5, $6)`,
			rejectedRecord.BatchID,
			rejectedRecord.UserID,
			rejectedRecord.SourceSystem,
			string(rawPayload),
			rejectedRecord.RejectionReason,
			string(fieldErrors),
		); err != nil {
			return SourceBatchIngestResult{}, err
		}
	}

	status := batch.Status
	if status == "" {
		status = sourceBatchStatus(len(records), len(rejected))
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE source_batches
		 SET status = $1,
		     record_count_inserted = $2,
		     record_count_updated = $3,
		     record_count_rejected = $4
		 WHERE id = $5`,
		status,
		inserted,
		updated,
		len(rejected),
		batch.ID,
	); err != nil {
		return SourceBatchIngestResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return SourceBatchIngestResult{}, err
	}
	return SourceBatchIngestResult{
		BatchID:          batch.ID,
		Status:           status,
		Received:         batch.RecordCountReceived,
		Inserted:         inserted,
		Updated:          updated,
		Rejected:         len(rejected),
		ProcessingStatus: "queued",
	}, nil
}

func (p *Postgres) ListSourceRecordsForBatch(ctx context.Context, userID, batchID string) ([]SourceRecord, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, batch_id, source_system, source_record_type, COALESCE(source_record_subtype, ''),
		        source_record_id, dedupe_key, start_time, end_time, observed_at, value, COALESCE(unit, ''),
		        raw_payload_json, normalized_payload_json, source_metadata_json, COALESCE(trust_level, ''),
		        COALESCE(schema_version, ''), COALESCE(normalization_version, '')
		 FROM source_records
		 WHERE user_id = $1 AND batch_id = $2
		 ORDER BY COALESCE(start_time, observed_at, created_at), source_record_type, source_record_subtype`,
		userID,
		batchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceRecord
	for rows.Next() {
		record, err := scanSourceRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertProgressContributions(ctx context.Context, contributions []ProgressContribution) error {
	if len(contributions) == 0 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, contribution := range contributions {
		if contribution.ID == "" || contribution.UserID == "" || contribution.TargetType == "" || contribution.ContributionType == "" {
			return errors.New("progress contribution requires id, user_id, target_type, and contribution_type")
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO progress_contributions (
				id, user_id, source_record_id, target_type, target_id, contribution_type, amount, unit,
				confidence, mapping_rule, mapping_rule_version, mapper_version, is_manual_override, manual_override_id
			 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			 ON CONFLICT (id) DO UPDATE SET
				source_record_id = EXCLUDED.source_record_id,
				target_type = EXCLUDED.target_type,
				target_id = EXCLUDED.target_id,
				contribution_type = EXCLUDED.contribution_type,
				amount = EXCLUDED.amount,
				unit = EXCLUDED.unit,
				confidence = EXCLUDED.confidence,
				mapping_rule = EXCLUDED.mapping_rule,
				mapping_rule_version = EXCLUDED.mapping_rule_version,
				mapper_version = EXCLUDED.mapper_version,
				is_manual_override = EXCLUDED.is_manual_override,
				manual_override_id = EXCLUDED.manual_override_id,
				updated_at = NOW()`,
			contribution.ID,
			contribution.UserID,
			nullableString(contribution.SourceRecordID),
			contribution.TargetType,
			nullableString(contribution.TargetID),
			contribution.ContributionType,
			nullableFloat(contribution.Amount),
			nullableString(contribution.Unit),
			nullableFloat(contribution.Confidence),
			nullableString(contribution.MappingRule),
			nullableString(contribution.MappingRuleVersion),
			nullableString(contribution.MapperVersion),
			contribution.IsManualOverride,
			nullableString(contribution.ManualOverrideID),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) UpsertDailyRollup(ctx context.Context, rollup *DailyRollup) error {
	if rollup == nil || rollup.ID == "" || rollup.UserID == "" || rollup.Date.IsZero() {
		return errors.New("daily rollup requires id, user_id, and date")
	}
	sourceSystems := rollup.SourceSystemsIncluded
	if sourceSystems == nil {
		sourceSystems = []string{}
	}
	sourceSystemsRaw, err := json.Marshal(sourceSystems)
	if err != nil {
		return err
	}
	structuredRollupRaw, err := marshalMap(rollup.StructuredRollup)
	if err != nil {
		return err
	}
	unmappedRaw, err := marshalMap(rollup.UnmappedRecordsSummary)
	if err != nil {
		return err
	}
	conflicts := rollup.Conflicts
	if conflicts == nil {
		conflicts = []map[string]any{}
	}
	conflictsRaw, err := json.Marshal(conflicts)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO daily_rollups (
			id, user_id, date, source_systems_included, structured_rollup_json, summary_text, lookahead_text,
			unmapped_records_summary_json, conflicts_json, rollup_version, summary_generation_version
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (user_id, date, rollup_version) DO UPDATE SET
			source_systems_included = EXCLUDED.source_systems_included,
			structured_rollup_json = EXCLUDED.structured_rollup_json,
			summary_text = EXCLUDED.summary_text,
			lookahead_text = EXCLUDED.lookahead_text,
			unmapped_records_summary_json = EXCLUDED.unmapped_records_summary_json,
			conflicts_json = EXCLUDED.conflicts_json,
			summary_generation_version = EXCLUDED.summary_generation_version,
			updated_at = NOW()`,
		rollup.ID,
		rollup.UserID,
		rollup.Date,
		string(sourceSystemsRaw),
		string(structuredRollupRaw),
		nullableString(rollup.SummaryText),
		nullableString(rollup.LookaheadText),
		string(unmappedRaw),
		string(conflictsRaw),
		nullableString(rollup.RollupVersion),
		nullableString(rollup.SummaryGenerationVersion),
	)
	return err
}

func (p *Postgres) ListDailyRollups(ctx context.Context, userID string, startDate, endDate time.Time) ([]DailyRollup, error) {
	if userID == "" || startDate.IsZero() || endDate.IsZero() {
		return nil, errors.New("daily rollup query requires user_id, start_date, and end_date")
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, date, source_systems_included, structured_rollup_json,
		        COALESCE(summary_text, ''), COALESCE(lookahead_text, ''), unmapped_records_summary_json,
		        conflicts_json, COALESCE(rollup_version, ''), COALESCE(summary_generation_version, ''),
		        created_at, updated_at
		 FROM daily_rollups
		 WHERE user_id = $1 AND date BETWEEN $2::date AND $3::date
		 ORDER BY date DESC, updated_at DESC`,
		userID,
		startDate,
		endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailyRollup
	for rows.Next() {
		rollup, err := scanDailyRollup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rollup)
	}
	return out, rows.Err()
}

func (p *Postgres) ListUnmappedSourceRecords(ctx context.Context, userID string, startDate, endDate time.Time, limit int) ([]SourceRecord, error) {
	if userID == "" || startDate.IsZero() || endDate.IsZero() {
		return nil, errors.New("unmapped source record query requires user_id, start_date, and end_date")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT sr.id, sr.user_id, sr.batch_id, sr.source_system, sr.source_record_type, COALESCE(sr.source_record_subtype, ''),
		        sr.source_record_id, sr.dedupe_key, sr.start_time, sr.end_time, sr.observed_at, sr.value, COALESCE(sr.unit, ''),
		        sr.raw_payload_json, sr.normalized_payload_json, sr.source_metadata_json, COALESCE(sr.trust_level, ''),
		        COALESCE(sr.schema_version, ''), COALESCE(sr.normalization_version, '')
		 FROM source_records sr
		 WHERE sr.user_id = $1
		   AND COALESCE(sr.start_time, sr.observed_at, sr.end_time, sr.created_at)::date BETWEEN $2::date AND $3::date
		   AND NOT EXISTS (
		     SELECT 1 FROM progress_contributions pc
		     WHERE pc.user_id = sr.user_id AND pc.source_record_id = sr.id
		   )
		 ORDER BY COALESCE(sr.start_time, sr.observed_at, sr.end_time, sr.created_at) DESC, sr.source_record_type, sr.source_record_subtype
		 LIMIT $4`,
		userID,
		startDate,
		endDate,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceRecord
	for rows.Next() {
		record, err := scanSourceRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanDailyRollup(rows *sql.Rows) (DailyRollup, error) {
	var rollup DailyRollup
	var sourceSystemsRaw, structuredRaw, unmappedRaw, conflictsRaw []byte
	if err := rows.Scan(
		&rollup.ID,
		&rollup.UserID,
		&rollup.Date,
		&sourceSystemsRaw,
		&structuredRaw,
		&rollup.SummaryText,
		&rollup.LookaheadText,
		&unmappedRaw,
		&conflictsRaw,
		&rollup.RollupVersion,
		&rollup.SummaryGenerationVersion,
		&rollup.CreatedAt,
		&rollup.UpdatedAt,
	); err != nil {
		return DailyRollup{}, err
	}
	if len(sourceSystemsRaw) > 0 {
		if err := json.Unmarshal(sourceSystemsRaw, &rollup.SourceSystemsIncluded); err != nil {
			return DailyRollup{}, err
		}
	}
	if len(structuredRaw) > 0 {
		if err := json.Unmarshal(structuredRaw, &rollup.StructuredRollup); err != nil {
			return DailyRollup{}, err
		}
	}
	if len(unmappedRaw) > 0 {
		if err := json.Unmarshal(unmappedRaw, &rollup.UnmappedRecordsSummary); err != nil {
			return DailyRollup{}, err
		}
	}
	if len(conflictsRaw) > 0 {
		if err := json.Unmarshal(conflictsRaw, &rollup.Conflicts); err != nil {
			return DailyRollup{}, err
		}
	}
	return rollup, nil
}

func scanSourceRecord(rows *sql.Rows) (SourceRecord, error) {
	var record SourceRecord
	var startTime, endTime, observedAt sql.NullTime
	var value sql.NullFloat64
	var rawPayloadRaw, normalizedPayloadRaw, sourceMetadataRaw []byte
	if err := rows.Scan(
		&record.ID,
		&record.UserID,
		&record.BatchID,
		&record.SourceSystem,
		&record.SourceRecordType,
		&record.SourceRecordSubtype,
		&record.SourceRecordID,
		&record.DedupeKey,
		&startTime,
		&endTime,
		&observedAt,
		&value,
		&record.Unit,
		&rawPayloadRaw,
		&normalizedPayloadRaw,
		&sourceMetadataRaw,
		&record.TrustLevel,
		&record.SchemaVersion,
		&record.NormalizationVersion,
	); err != nil {
		return SourceRecord{}, err
	}
	if startTime.Valid {
		t := startTime.Time
		record.StartTime = &t
	}
	if endTime.Valid {
		t := endTime.Time
		record.EndTime = &t
	}
	if observedAt.Valid {
		t := observedAt.Time
		record.ObservedAt = &t
	}
	if value.Valid {
		v := value.Float64
		record.Value = &v
	}
	if len(rawPayloadRaw) > 0 {
		if err := json.Unmarshal(rawPayloadRaw, &record.RawPayload); err != nil {
			return SourceRecord{}, err
		}
	}
	if len(normalizedPayloadRaw) > 0 {
		if err := json.Unmarshal(normalizedPayloadRaw, &record.NormalizedPayload); err != nil {
			return SourceRecord{}, err
		}
	}
	if len(sourceMetadataRaw) > 0 {
		if err := json.Unmarshal(sourceMetadataRaw, &record.SourceMetadata); err != nil {
			return SourceRecord{}, err
		}
	}
	return record, nil
}

func marshalMap(value map[string]any) ([]byte, error) {
	if value == nil {
		value = map[string]any{}
	}
	return json.Marshal(value)
}

func nullableFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func sourceBatchStatus(accepted, rejected int) string {
	switch {
	case accepted > 0 && rejected > 0:
		return "partially_accepted"
	case accepted > 0:
		return "accepted"
	default:
		return "rejected"
	}
}
