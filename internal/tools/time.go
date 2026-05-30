package tools

import (
	"context"
	"time"
)

// TimeNowTool returns the current UTC time and operator-local time. No params.
type TimeNowTool struct {
	// Location is the operator's local IANA timezone (e.g. "America/New_York"),
	// used for the human-friendly "local" field. Defaults to UTC if empty or
	// invalid.
	Location string
	// Now optionally overrides the current time source for tests.
	Now func() time.Time
}

func (t *TimeNowTool) Definition() Tool {
	return Tool{
		Name:        "time_now",
		Description: "Returns the current date and time in the operator's local timezone, plus UTC as a reference and whether today is a US federal holiday. Call this whenever the user asks for the current time, date, day of week, or anything time-sensitive. For user-facing answers, use local_date/local_weekday/local_time; use utc only when the user explicitly asks for UTC.",
	}
}

func (t *TimeNowTool) Execute(_ context.Context, _ map[string]any) (any, error) {
	nowFunc := t.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	now := nowFunc().UTC()
	out := map[string]any{
		"utc_reference_only":         now.Format(time.RFC3339),
		"utc_weekday_reference_only": now.Weekday().String(),
		"epoch":                      now.Unix(),
		"use_local_for_user_day":     true,
	}
	loc := t.Location
	if loc == "" {
		loc = "UTC"
	}
	var localForHoliday time.Time
	if l, err := time.LoadLocation(loc); err == nil {
		local := now.In(l)
		localForHoliday = local
		out["answer_date"] = local.Format("Monday, January 2, 2006")
		out["local"] = local.Format("2006-01-02 15:04:05 MST")
		out["local_date"] = local.Format("2006-01-02")
		out["local_time"] = local.Format("15:04:05")
		out["local_timezone"] = loc
		out["local_timezone_abbreviation"] = local.Format("MST")
		out["local_utc_offset"] = local.Format("-07:00")
		out["local_weekday"] = local.Weekday().String()
		out["weekday"] = local.Weekday().String()
	} else {
		localForHoliday = now
		out["answer_date"] = now.Format("Monday, January 2, 2006")
		out["local"] = now.Format("2006-01-02 15:04:05 MST")
		out["local_date"] = now.Format("2006-01-02")
		out["local_time"] = now.Format("15:04:05")
		out["local_timezone"] = "UTC"
		out["local_timezone_abbreviation"] = "UTC"
		out["local_utc_offset"] = "+00:00"
		out["local_weekday"] = now.Weekday().String()
		out["weekday"] = now.Weekday().String()
	}
	if name := USFederalHoliday(localForHoliday); name != "" {
		out["is_us_federal_holiday"] = true
		out["us_federal_holiday"] = name
	} else {
		out["is_us_federal_holiday"] = false
	}
	return out, nil
}
