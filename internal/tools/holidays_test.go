package tools

import (
	"testing"
	"time"
)

func TestUSFederalHoliday(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		date string
		want string
	}{
		// Fixed
		{"2026-01-01", "New Year's Day"},
		{"2026-06-19", "Juneteenth National Independence Day"},
		{"2026-07-04", "Independence Day"}, // Saturday
		{"2026-07-03", "Independence Day (observed)"},
		{"2026-12-25", "Christmas Day"},
		{"2026-11-11", "Veterans Day"},
		// Floating
		{"2026-01-19", "Martin Luther King Jr. Day"}, // 3rd Mon
		{"2026-02-16", "Presidents' Day"},             // 3rd Mon
		{"2026-05-25", "Memorial Day"},                // last Mon
		{"2026-09-07", "Labor Day"},                   // 1st Mon
		{"2026-10-12", "Columbus Day"},                // 2nd Mon
		{"2026-11-26", "Thanksgiving Day"},            // 4th Thu
		// Negative
		{"2026-05-18", ""},
		{"2026-07-05", ""},
		{"2026-12-26", ""},
	}
	for _, tc := range cases {
		d, err := time.ParseInLocation("2006-01-02", tc.date, loc)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.date, err)
		}
		got := USFederalHoliday(d)
		if got != tc.want {
			t.Errorf("USFederalHoliday(%s) = %q, want %q", tc.date, got, tc.want)
		}
	}
}
