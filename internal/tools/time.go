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
}

func (t *TimeNowTool) Definition() Tool {
	return Tool{
		Name:        "time_now",
		Description: "Returns the current date and time in UTC and the operator's local timezone, plus whether today is a US federal holiday. Call this whenever the user asks for the current time, date, day of week, or anything time-sensitive — and consult is_us_federal_holiday / us_federal_holiday before assuming today is a normal working day.",
	}
}

func (t *TimeNowTool) Execute(_ context.Context, _ map[string]any) (any, error) {
	now := time.Now().UTC()
	out := map[string]any{
		"utc":   now.Format(time.RFC3339),
		"epoch": now.Unix(),
	}
	loc := t.Location
	if loc == "" {
		loc = "UTC"
	}
	var localForHoliday time.Time
	if l, err := time.LoadLocation(loc); err == nil {
		local := now.In(l)
		localForHoliday = local
		out["local"] = local.Format("2006-01-02 15:04:05 MST")
		out["local_timezone"] = loc
		out["weekday"] = local.Weekday().String()
	} else {
		localForHoliday = now
		out["local"] = now.Format("2006-01-02 15:04:05 MST")
		out["local_timezone"] = "UTC"
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
