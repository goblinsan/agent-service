package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

type DailyRollupTool struct {
	Store    store.DailyRollupStore
	Location string
}

func (t *DailyRollupTool) Definition() Tool {
	return Tool{
		Name:        "get_daily_rollup",
		Description: "Returns the current user's deterministic personal-data daily rollup for one local date. Use this for health, nutrition, activity, sleep, or other imported personal-data progress before asking the model to infer from raw records.",
		Params: []Param{
			{Name: "date", Type: "string", Description: "Local date in YYYY-MM-DD format. Defaults to today when omitted.", Required: false},
		},
	}
}

func (t *DailyRollupTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("daily rollup store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return map[string]any{"rollup": nil, "note": "no authenticated user on this run"}, nil
	}
	date, err := localToolDate(params["date"], t.Location)
	if err != nil {
		return nil, err
	}
	rollups, err := t.Store.ListDailyRollups(ctx, uid, date, date)
	if err != nil {
		return nil, fmt.Errorf("list daily rollups: %w", err)
	}
	if len(rollups) == 0 {
		return map[string]any{"date": date.Format("2006-01-02"), "rollup": nil}, nil
	}
	return map[string]any{"date": date.Format("2006-01-02"), "rollup": summarizeDailyRollup(rollups[0])}, nil
}

type UnmappedRecordsTool struct {
	Store    store.UnmappedSourceRecordStore
	Location string
}

func (t *UnmappedRecordsTool) Definition() Tool {
	return Tool{
		Name:        "get_unmapped_records",
		Description: "Returns accepted personal-data source records in a local date range that have not been mapped to progress contributions. Use this to inspect unresolved records before explaining mapping gaps or proposing corrections.",
		Params: []Param{
			{Name: "start_date", Type: "string", Description: "Inclusive local start date in YYYY-MM-DD format. Defaults to today.", Required: false},
			{Name: "end_date", Type: "string", Description: "Inclusive local end date in YYYY-MM-DD format. Defaults to start_date.", Required: false},
			{Name: "limit", Type: "int", Description: "Maximum number of records to return, capped at 100. Defaults to 50.", Required: false},
		},
	}
}

func (t *UnmappedRecordsTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("unmapped source record store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return map[string]any{"records": []any{}, "note": "no authenticated user on this run"}, nil
	}
	start, err := localToolDate(params["start_date"], t.Location)
	if err != nil {
		return nil, fmt.Errorf("start_date: %w", err)
	}
	endParam := params["end_date"]
	if strings.TrimSpace(fmt.Sprint(endParam)) == "" || endParam == nil {
		endParam = start.Format("2006-01-02")
	}
	end, err := localToolDate(endParam, t.Location)
	if err != nil {
		return nil, fmt.Errorf("end_date: %w", err)
	}
	if end.Before(start) {
		return nil, errors.New("end_date must be on or after start_date")
	}
	limit := personalDataIntParam(params["limit"], 50)
	records, err := t.Store.ListUnmappedSourceRecords(ctx, uid, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("list unmapped source records: %w", err)
	}
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out = append(out, summarizeSourceRecord(record))
	}
	return map[string]any{
		"start_date": start.Format("2006-01-02"),
		"end_date":   end.Format("2006-01-02"),
		"count":      len(out),
		"records":    out,
	}, nil
}

func localToolDate(raw any, location string) (time.Time, error) {
	dateText, _ := raw.(string)
	dateText = strings.TrimSpace(dateText)
	if dateText == "" {
		now := time.Now().In(resolveLocation(location))
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	parsed, err := time.Parse("2006-01-02", dateText)
	if err != nil {
		return time.Time{}, errors.New("date must use YYYY-MM-DD")
	}
	return parsed, nil
}

func personalDataIntParam(raw any, fallback int) int {
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func summarizeDailyRollup(rollup store.DailyRollup) map[string]any {
	return map[string]any{
		"id":                            rollup.ID,
		"date":                          rollup.Date.Format("2006-01-02"),
		"source_systems_included":       rollup.SourceSystemsIncluded,
		"structured_rollup_json":        rollup.StructuredRollup,
		"summary_text":                  rollup.SummaryText,
		"lookahead_text":                rollup.LookaheadText,
		"unmapped_records_summary_json": rollup.UnmappedRecordsSummary,
		"conflicts_json":                rollup.Conflicts,
		"rollup_version":                rollup.RollupVersion,
		"summary_generation_version":    rollup.SummaryGenerationVersion,
		"updated_at":                    rollup.UpdatedAt,
	}
}

func summarizeSourceRecord(record store.SourceRecord) map[string]any {
	var value any
	if record.Value != nil {
		value = *record.Value
	}
	out := map[string]any{
		"id":                      record.ID,
		"batch_id":                record.BatchID,
		"source_system":           record.SourceSystem,
		"source_record_type":      record.SourceRecordType,
		"source_record_subtype":   record.SourceRecordSubtype,
		"source_record_id":        record.SourceRecordID,
		"start_time":              formatOptionalTime(record.StartTime),
		"end_time":                formatOptionalTime(record.EndTime),
		"observed_at":             formatOptionalTime(record.ObservedAt),
		"value":                   value,
		"unit":                    record.Unit,
		"normalized_payload_json": record.NormalizedPayload,
		"source_metadata_json":    record.SourceMetadata,
		"trust_level":             record.TrustLevel,
		"schema_version":          record.SchemaVersion,
		"normalization_version":   record.NormalizationVersion,
	}
	return out
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}
