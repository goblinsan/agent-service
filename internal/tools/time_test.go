package tools

import (
	"context"
	"testing"
	"time"
)

func TestTimeNowToolReturnsLocalDateAsAuthoritative(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-05-28T00:37:00Z")
	if err != nil {
		t.Fatal(err)
	}
	tool := &TimeNowTool{
		Location: "America/New_York",
		Now: func() time.Time {
			return now
		},
	}

	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)

	checks := map[string]any{
		"answer_date":                 "Wednesday, May 27, 2026",
		"local":                       "2026-05-27 20:37:00 EDT",
		"local_date":                  "2026-05-27",
		"local_time":                  "20:37:00",
		"local_weekday":               "Wednesday",
		"local_timezone":              "America/New_York",
		"local_timezone_abbreviation": "EDT",
		"local_utc_offset":            "-04:00",
		"utc_reference_only":          "2026-05-28T00:37:00Z",
		"utc_weekday_reference_only":  "Thursday",
		"use_local_for_user_day":      true,
	}
	for key, want := range checks {
		if got := out[key]; got != want {
			t.Fatalf("%s = %#v, want %#v; full output: %#v", key, got, want, out)
		}
	}
}
