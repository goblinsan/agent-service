package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
	"github.com/goblinsan/agent-service/internal/tools"
)

type AppleHealthSummaryRequest struct {
	Date      string         `json:"date"`
	Timezone  string         `json:"timezone"`
	Activity  map[string]any `json:"activity"`
	Nutrition map[string]any `json:"nutrition"`
}

func (s *Service) IngestAppleHealthSummary(ctx context.Context, userID string, req AppleHealthSummaryRequest) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("service store not configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user id is required")
	}

	result := map[string]any{"status": "ok"}
	if sourceBatch, err := s.ingestAppleHealthSummarySourceRecords(ctx, userID, req); err == nil {
		result["source_batch"] = sourceBatch
	} else if !errors.Is(err, errPersonalDataStoreUnsupported) {
		result["source_batch_error"] = err.Error()
	}
	activityMetrics := enrichAppleHealthMetrics(req.Activity, req)
	if len(activityMetrics) > 0 {
		activityTool := &tools.PersonalDataIngestTool{
			Store:         s.store,
			App:           "apple-health",
			Domain:        "health",
			ToolName:      "apple_health_ingest_activity",
			ToolDesc:      "Ingest Apple Health activity summary.",
			PlanTitle:     "Health goals & activity",
			DefaultSource: "Apple Health",
			EventKind:     "health_sync",
		}
		out, err := activityTool.Execute(tools.WithUserID(ctx, userID), map[string]any{
			"source":         "Apple Health",
			"summary":        appleHealthSummary("activity", activityMetrics, req),
			"metrics":        activityMetrics,
			"review_cadence": "daily",
		})
		if err != nil {
			return nil, err
		}
		result["activity"] = out
	}

	nutritionMetrics := enrichAppleHealthMetrics(req.Nutrition, req)
	if len(nutritionMetrics) > 0 {
		nutritionTool := &tools.PersonalDataIngestTool{
			Store:         s.store,
			App:           "apple-health",
			Domain:        "nutrition",
			ToolName:      "apple_health_ingest_nutrition",
			ToolDesc:      "Ingest Apple Health nutrition summary.",
			PlanTitle:     "Nutrition goals & intake",
			DefaultSource: "Apple Health",
			EventKind:     "nutrition_sync",
		}
		out, err := nutritionTool.Execute(tools.WithUserID(ctx, userID), map[string]any{
			"source":         "Apple Health",
			"summary":        appleHealthSummary("nutrition", nutritionMetrics, req),
			"metrics":        nutritionMetrics,
			"review_cadence": "daily",
		})
		if err != nil {
			return nil, err
		}
		result["nutrition"] = out
	}

	if _, ok := result["activity"]; !ok {
		if _, ok := result["nutrition"]; !ok {
			return nil, fmt.Errorf("activity or nutrition metrics are required")
		}
	}
	return result, nil
}

func (s *Service) ingestAppleHealthSummarySourceRecords(ctx context.Context, userID string, req AppleHealthSummaryRequest) (store.SourceBatchIngestResult, error) {
	records := appleHealthSummaryRecords(req)
	if len(records) == 0 {
		return store.SourceBatchIngestResult{}, errPersonalDataStoreUnsupported
	}
	return s.IngestPersonalDataBatch(ctx, userID, PersonalDataBatchRequest{
		SourceSystem:         "apple_healthkit",
		SourceApp:            "Apple Health",
		SchemaVersion:        "apple-health-summary.v1",
		NormalizationVersion: "agent-service.healthkit-summary.v1",
		Metadata: map[string]any{
			"legacy_summary_endpoint": true,
			"date":                    strings.TrimSpace(req.Date),
			"timezone":                strings.TrimSpace(req.Timezone),
		},
		Records: records,
	})
}

func appleHealthSummaryRecords(req AppleHealthSummaryRequest) []PersonalDataRecordRequest {
	start, end := appleHealthDayBounds(req.Date, req.Timezone)
	records := make([]PersonalDataRecordRequest, 0, len(req.Activity)+len(req.Nutrition))
	for key, value := range req.Activity {
		amount, ok := numericValue(value)
		if !ok {
			continue
		}
		records = append(records, appleHealthMetricRecord(req, "health.activity", key, amount, appleHealthMetricUnit(key), start, end))
	}
	for key, value := range req.Nutrition {
		amount, ok := numericValue(value)
		if !ok {
			continue
		}
		records = append(records, appleHealthMetricRecord(req, "health.nutrition", key, amount, appleHealthMetricUnit(key), start, end))
	}
	return records
}

func appleHealthMetricRecord(req AppleHealthSummaryRequest, recordType, key string, value float64, unit string, start, end string) PersonalDataRecordRequest {
	date := strings.TrimSpace(req.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	sourceRecordID := strings.Join([]string{"apple_healthkit", date, recordType, key}, ":")
	return PersonalDataRecordRequest{
		SourceRecordType:    recordType,
		SourceRecordSubtype: key,
		SourceRecordID:      sourceRecordID,
		DedupeKey:           sourceRecordID,
		StartTime:           start,
		EndTime:             end,
		Value:               &value,
		Unit:                unit,
		NormalizedPayload: map[string]any{
			"date":     date,
			"metric":   key,
			"value":    value,
			"unit":     unit,
			"timezone": strings.TrimSpace(req.Timezone),
		},
		SourceMetadata: map[string]any{
			"source": "Apple Health",
		},
		TrustLevel: "app_reported",
	}
}

func appleHealthDayBounds(dateValue, timezone string) (string, string) {
	dateValue = strings.TrimSpace(dateValue)
	if dateValue == "" {
		return "", ""
	}
	loc := time.Local
	if timezone = strings.TrimSpace(timezone); timezone != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			loc = loaded
		}
	}
	start, err := time.ParseInLocation("2006-01-02", dateValue, loc)
	if err != nil {
		return "", ""
	}
	end := start.AddDate(0, 0, 1)
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func appleHealthMetricUnit(key string) string {
	switch {
	case strings.Contains(key, "minutes"):
		return "min"
	case strings.Contains(key, "kcal"):
		return "kcal"
	case strings.Contains(key, "miles"):
		return "mile"
	case strings.Contains(key, "grams"):
		return "g"
	case strings.Contains(key, "ounces"):
		return "fl_oz"
	case strings.Contains(key, "weight_lb"):
		return "lb"
	case strings.Contains(key, "heart_rate") || strings.Contains(key, "bpm"):
		return "bpm"
	default:
		return "count"
	}
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func enrichAppleHealthMetrics(input map[string]any, req AppleHealthSummaryRequest) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return out
	}
	if strings.TrimSpace(req.Date) != "" {
		out["date"] = strings.TrimSpace(req.Date)
	}
	if strings.TrimSpace(req.Timezone) != "" {
		out["timezone"] = strings.TrimSpace(req.Timezone)
	}
	return out
}

func appleHealthSummary(kind string, metrics map[string]any, req AppleHealthSummaryRequest) string {
	parts := []string{"Apple Health " + kind + " sync"}
	if strings.TrimSpace(req.Date) != "" {
		parts = append(parts, "date="+strings.TrimSpace(req.Date))
	}
	for _, key := range []string{
		"workouts", "workout_minutes", "exercise_minutes", "steps", "active_energy_kcal",
		"distance_miles", "dietary_energy_kcal", "protein_grams", "carbs_grams", "fat_grams", "water_ounces", "weight_lb",
	} {
		if value, ok := metrics[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return strings.Join(parts, "; ")
}
