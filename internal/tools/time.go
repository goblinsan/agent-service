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
		Description: "Returns the current date and time in UTC and the operator's local timezone. Call this whenever the user asks for the current time, date, day of week, or anything time-sensitive.",
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
	if l, err := time.LoadLocation(loc); err == nil {
		local := now.In(l)
		out["local"] = local.Format("2006-01-02 15:04:05 MST")
		out["local_timezone"] = loc
		out["weekday"] = local.Weekday().String()
	} else {
		out["local"] = now.Format("2006-01-02 15:04:05 MST")
		out["local_timezone"] = "UTC"
		out["weekday"] = now.Weekday().String()
	}
	return out, nil
}
