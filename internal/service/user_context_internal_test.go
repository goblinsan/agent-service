package service

import (
	"strings"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

func TestAppendCurrentTimeContextUsesUserLocalDateBeforeUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-05-28T00:37:00Z")
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	localNow := appendCurrentTimeContext(&b, now, loc)
	context := b.String()

	if got := localNow.Format("2006-01-02 15:04:05 MST"); got != "2026-05-27 20:37:00 EDT" {
		t.Fatalf("localNow = %s", got)
	}
	for _, want := range []string{
		"Authoritative user-local date/time: Wednesday, May 27, 2026 at 20:37:00 EDT",
		"local_date=2026-05-27; local_weekday=Wednesday; local_time=20:37:00; local_timezone=America/New_York; local_timezone_abbreviation=EDT",
		"UTC reference only, not the user's local day unless explicitly requested: utc_datetime=2026-05-28T00:37:00Z; utc_weekday=Thursday",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("context missing %q:\n%s", want, context)
		}
	}
}

func TestAppendDailyRollupContextSummarizesRecentRollups(t *testing.T) {
	localNow, err := time.Parse(time.RFC3339, "2026-05-29T09:00:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", "2026-05-29")
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	appendDailyRollupContext(&b, []store.DailyRollup{{
		Date:                  date,
		SourceSystemsIncluded: []string{"apple_healthkit"},
		SummaryText:           "Mapped 3 Apple Health records for 2026-05-29.",
		LookaheadText:         "Keep protein steady tonight.",
		StructuredRollup: map[string]any{
			"totals": map[string]any{
				"protein_grams": map[string]any{"value": 120.0, "unit": "g"},
				"steps":         map[string]any{"value": 8400.0, "unit": "count"},
			},
		},
		UnmappedRecordsSummary: map[string]any{"count": 2.0},
	}}, localNow)
	context := b.String()

	for _, want := range []string{
		"Personal data rollups:",
		"- today (2026-05-29): Mapped 3 Apple Health records for 2026-05-29. [sources: apple_healthkit]",
		"protein_grams=120.00 g",
		"steps=8400.00 count",
		"lookahead: Keep protein steady tonight.",
		"unresolved: 2 unmapped accepted records",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("context missing %q:\n%s", want, context)
		}
	}
}
