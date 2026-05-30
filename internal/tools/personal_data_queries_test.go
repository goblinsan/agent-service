package tools

import (
	"context"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

type fakePersonalDataQueryStore struct {
	rollups             []store.DailyRollup
	unmapped            []store.SourceRecord
	lastRollupStartDate time.Time
	lastRollupEndDate   time.Time
	lastUnmappedStart   time.Time
	lastUnmappedEnd     time.Time
	lastUnmappedLimit   int
}

func (f *fakePersonalDataQueryStore) ListDailyRollups(_ context.Context, _ string, startDate, endDate time.Time) ([]store.DailyRollup, error) {
	f.lastRollupStartDate = startDate
	f.lastRollupEndDate = endDate
	return f.rollups, nil
}

func (f *fakePersonalDataQueryStore) ListUnmappedSourceRecords(_ context.Context, _ string, startDate, endDate time.Time, limit int) ([]store.SourceRecord, error) {
	f.lastUnmappedStart = startDate
	f.lastUnmappedEnd = endDate
	f.lastUnmappedLimit = limit
	return f.unmapped, nil
}

func TestDailyRollupToolReturnsRollupForDate(t *testing.T) {
	date, err := time.Parse("2006-01-02", "2026-05-29")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakePersonalDataQueryStore{rollups: []store.DailyRollup{{
		ID:                    "rollup-1",
		Date:                  date,
		SourceSystemsIncluded: []string{"apple_healthkit"},
		StructuredRollup: map[string]any{
			"totals": map[string]any{"steps": map[string]any{"value": 8400.0, "unit": "count"}},
		},
		SummaryText:   "Mapped 2 Apple Health records.",
		RollupVersion: "daily-rollup.v1",
	}}}
	tool := &DailyRollupTool{Store: fs, Location: "America/New_York"}
	res, err := tool.Execute(WithUserID(context.Background(), "u1"), map[string]any{"date": "2026-05-29"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fs.lastRollupStartDate.Format("2006-01-02"); got != "2026-05-29" {
		t.Fatalf("queried start date %s", got)
	}
	rollup := res.(map[string]any)["rollup"].(map[string]any)
	if rollup["summary_text"] != "Mapped 2 Apple Health records." {
		t.Fatalf("unexpected rollup %#v", rollup)
	}
	if rollup["rollup_version"] != "daily-rollup.v1" {
		t.Fatalf("unexpected rollup version %#v", rollup)
	}
}

func TestDailyRollupToolNoUserDoesNotQuery(t *testing.T) {
	fs := &fakePersonalDataQueryStore{}
	tool := &DailyRollupTool{Store: fs}
	res, err := tool.Execute(context.Background(), map[string]any{"date": "2026-05-29"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["note"] == nil {
		t.Fatalf("expected missing-user note, got %#v", res)
	}
}

func TestUnmappedRecordsToolReturnsCompactRecords(t *testing.T) {
	startTime, err := time.Parse(time.RFC3339, "2026-05-29T10:00:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	value := 7.5
	fs := &fakePersonalDataQueryStore{unmapped: []store.SourceRecord{{
		ID:                  "sr-1",
		BatchID:             "batch-1",
		SourceSystem:        "apple_healthkit",
		SourceRecordType:    "health.recovery",
		SourceRecordSubtype: "resting_heart_rate",
		SourceRecordID:      "hk-1",
		StartTime:           &startTime,
		Value:               &value,
		Unit:                "bpm",
		NormalizedPayload:   map[string]any{"metric": "resting_heart_rate"},
		RawPayload:          map[string]any{"private": "not returned"},
		TrustLevel:          "device_measured",
	}}}
	tool := &UnmappedRecordsTool{Store: fs, Location: "America/New_York"}
	res, err := tool.Execute(WithUserID(context.Background(), "u1"), map[string]any{
		"start_date": "2026-05-28",
		"end_date":   "2026-05-29",
		"limit":      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fs.lastUnmappedLimit; got != 10 {
		t.Fatalf("limit = %d", got)
	}
	records := res.(map[string]any)["records"].([]map[string]any)
	if len(records) != 1 {
		t.Fatalf("expected one record, got %#v", records)
	}
	if records[0]["raw_payload_json"] != nil {
		t.Fatalf("raw payload should not be returned by default: %#v", records[0])
	}
	if records[0]["value"] != 7.5 {
		t.Fatalf("unexpected value %#v", records[0]["value"])
	}
	if records[0]["normalized_payload_json"].(map[string]any)["metric"] != "resting_heart_rate" {
		t.Fatalf("unexpected normalized payload %#v", records[0])
	}
}

func TestUnmappedRecordsToolRejectsReversedRange(t *testing.T) {
	tool := &UnmappedRecordsTool{Store: &fakePersonalDataQueryStore{}}
	_, err := tool.Execute(WithUserID(context.Background(), "u1"), map[string]any{
		"start_date": "2026-05-29",
		"end_date":   "2026-05-28",
	})
	if err == nil {
		t.Fatal("expected reversed range error")
	}
}
