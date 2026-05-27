package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

type scheduleStore interface {
	CreateScheduledJob(ctx context.Context, job *store.ScheduledJob) error
}

var projectCheckinWindowTimes = map[string]string{
	"morning":   "08:00",
	"afternoon": "13:00",
	"night":     "20:00",
}

// ScheduleCreateTool creates a scheduled job for the current authenticated user.
// It is intended for reminders, follow-ups, and delayed agent work.
type ScheduleCreateTool struct {
	Store scheduleStore
}

func (t *ScheduleCreateTool) Definition() Tool {
	return Tool{
		Name:        "create_schedule",
		Description: "Create a reminder or delayed follow-up for the current user. Use this when the user asks to be reminded later or asks you to do something at a future time. Prefer delay_seconds for relative requests like 'in 1 minute'. If you know the exact wall-clock time, pass run_at as an RFC3339 timestamp. For recurring local-time reminders, pass recurrence and optionally timezone. The current thread and agent are reused automatically unless you override them.",
		Params: []Param{
			{Name: "prompt", Type: "string", Description: "The prompt that should run at the scheduled time, phrased as the future reminder or task itself.", Required: true},
			{Name: "delay_seconds", Type: "int", Description: "Relative delay in seconds from now. Prefer this for requests like 'in 1 minute'.", Required: false},
			{Name: "run_at", Type: "string", Description: "Absolute execution time as an RFC3339 timestamp. Use this instead of delay_seconds when the exact time is known.", Required: false},
			{Name: "recurrence", Type: "string", Description: "Optional recurrence, for example '@every 24h' or '@daily-local 08:00,13:00,20:00'. Leave empty for one-shot reminders.", Required: false},
			{Name: "timezone", Type: "string", Description: "Optional IANA timezone used by local-time recurrences, for example 'America/New_York'.", Required: false},
			{Name: "thread_id", Type: "string", Description: "Optional explicit thread id override. Defaults to the current thread.", Required: false},
			{Name: "agent_id", Type: "string", Description: "Optional explicit agent id override. Defaults to the current agent.", Required: false},
		},
	}
}

func (t *ScheduleCreateTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("schedule store not configured")
	}
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return nil, errors.New("no authenticated user on this run; cannot create schedule")
	}

	prompt, _ := params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}

	runAt, err := resolveScheduledRunAt(params)
	if err != nil {
		return nil, err
	}

	threadID, _ := params["thread_id"].(string)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = ThreadIDFromContext(ctx)
	}

	agentID, _ := params["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = AgentIDFromContext(ctx)
	}

	recurrence, _ := params["recurrence"].(string)
	recurrence = strings.TrimSpace(recurrence)
	timezone, _ := params["timezone"].(string)
	timezone = strings.TrimSpace(timezone)

	job := &store.ScheduledJob{
		ID:         newScheduleID(),
		UserID:     userID,
		Kind:       "reminder",
		Prompt:     prompt,
		ThreadID:   threadID,
		AgentID:    agentID,
		RunAt:      runAt.UTC(),
		Recurrence: recurrence,
		Timezone:   timezone,
		Status:     "pending",
	}
	if err := t.Store.CreateScheduledJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create schedule: %w", err)
	}

	return map[string]any{
		"status": "ok",
		"schedule": map[string]any{
			"id":         job.ID,
			"prompt":     job.Prompt,
			"run_at":     job.RunAt.Format(time.RFC3339),
			"recurrence": job.Recurrence,
			"timezone":   job.Timezone,
			"thread_id":  job.ThreadID,
			"agent_id":   job.AgentID,
		},
	}, nil
}

// ProjectCheckinCreateTool provisions a dedicated recurring project-manager
// check-in job with local-time recurrence semantics.
type ProjectCheckinCreateTool struct {
	Store scheduleStore
}

func (t *ProjectCheckinCreateTool) Definition() Tool {
	return Tool{
		Name:        "create_project_checkin",
		Description: "Create a recurring project-manager check-in for the current user. Use this when the user wants the assistant to check in every morning, afternoon, and/or night, review progress against goals, and suggest next actions. This creates a dedicated project_checkin scheduled job with local-time recurrence.",
		Params: []Param{
			{Name: "windows", Type: "array", Description: "One or more named daily check-in windows: morning, afternoon, night.", Required: true},
			{Name: "timezone", Type: "string", Description: "IANA timezone for the daily schedule, for example 'America/New_York'. Defaults to the operator timezone.", Required: false},
			{Name: "prompt", Type: "string", Description: "Optional custom check-in instructions. Leave empty to use the default project-manager prompt.", Required: false},
			{Name: "thread_id", Type: "string", Description: "Optional explicit thread id override. Defaults to the current thread.", Required: false},
			{Name: "agent_id", Type: "string", Description: "Optional explicit agent id override. Defaults to the current agent.", Required: false},
		},
	}
}

func (t *ProjectCheckinCreateTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("schedule store not configured")
	}
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return nil, errors.New("no authenticated user on this run; cannot create project check-in")
	}
	windows, err := normalizeStringList(params["windows"], "windows")
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return nil, errors.New("windows is required")
	}
	timezone, _ := params["timezone"].(string)
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "America/New_York"
	}
	windowTimes, recurrence, err := buildDailyLocalRecurrence(windows)
	if err != nil {
		return nil, err
	}
	runAt, err := resolveNextLocalRecurrenceRunAt(recurrence, timezone, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	threadID, _ := params["thread_id"].(string)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = ThreadIDFromContext(ctx)
	}
	agentID, _ := params["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = AgentIDFromContext(ctx)
	}
	prompt, _ := params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = defaultProjectCheckinPrompt(windows)
	}

	job := &store.ScheduledJob{
		ID:         newScheduleID(),
		UserID:     userID,
		Kind:       "project_checkin",
		Prompt:     prompt,
		ThreadID:   threadID,
		AgentID:    agentID,
		RunAt:      runAt.UTC(),
		Recurrence: recurrence,
		Timezone:   timezone,
		Status:     "pending",
		Payload: map[string]any{
			"windows": windows,
			"times":   windowTimes,
		},
	}
	if err := t.Store.CreateScheduledJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create project check-in: %w", err)
	}
	return map[string]any{
		"status": "ok",
		"schedule": map[string]any{
			"id":         job.ID,
			"kind":       job.Kind,
			"run_at":     job.RunAt.Format(time.RFC3339),
			"recurrence": job.Recurrence,
			"timezone":   job.Timezone,
			"windows":    windows,
			"times":      windowTimes,
			"thread_id":  job.ThreadID,
			"agent_id":   job.AgentID,
		},
	}, nil
}

func resolveScheduledRunAt(params map[string]any) (time.Time, error) {
	if raw, _ := params["run_at"].(string); strings.TrimSpace(raw) != "" {
		runAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if err != nil {
			return time.Time{}, fmt.Errorf("run_at must be RFC3339: %w", err)
		}
		return runAt, nil
	}

	switch v := params["delay_seconds"].(type) {
	case int:
		if v <= 0 {
			return time.Time{}, errors.New("delay_seconds must be positive")
		}
		return time.Now().UTC().Add(time.Duration(v) * time.Second), nil
	case int64:
		if v <= 0 {
			return time.Time{}, errors.New("delay_seconds must be positive")
		}
		return time.Now().UTC().Add(time.Duration(v) * time.Second), nil
	case float64:
		if v <= 0 {
			return time.Time{}, errors.New("delay_seconds must be positive")
		}
		return time.Now().UTC().Add(time.Duration(int(v)) * time.Second), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			break
		}
		var parsed int
		if _, err := fmt.Sscanf(s, "%d", &parsed); err != nil || parsed <= 0 {
			return time.Time{}, errors.New("delay_seconds must be a positive integer")
		}
		return time.Now().UTC().Add(time.Duration(parsed) * time.Second), nil
	}

	return time.Time{}, errors.New("either run_at or delay_seconds is required")
}

func buildDailyLocalRecurrence(windows []string) ([]string, string, error) {
	times := make([]string, 0, len(windows))
	seen := map[string]struct{}{}
	for _, window := range windows {
		key := strings.ToLower(strings.TrimSpace(window))
		if key == "" {
			continue
		}
		timeValue, ok := projectCheckinWindowTimes[key]
		if !ok {
			return nil, "", fmt.Errorf("unsupported window %q; expected morning, afternoon, or night", window)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		times = append(times, timeValue)
	}
	if len(times) == 0 {
		return nil, "", errors.New("at least one valid window is required")
	}
	return times, "@daily-local " + strings.Join(times, ","), nil
}

func defaultProjectCheckinPrompt(windows []string) string {
	windowLabel := strings.Join(windows, ", ")
	if strings.TrimSpace(windowLabel) == "" {
		windowLabel = "scheduled check-in"
	}
	return fmt.Sprintf(
		"You are running a recurring %s project-manager check-in. Review the user's active plans, recent events, and durable memory. Respond concisely with: 1) current priorities, 2) progress/wins, 3) blockers or risks, and 4) the single next best action. If the user has reported progress or priorities have changed, update the relevant plan(s) using plan_upsert.",
		windowLabel,
	)
}

func resolveNextLocalRecurrenceRunAt(recurrence, timezone string, now time.Time) (time.Time, error) {
	spec, err := parseDailyLocalRecurrence(recurrence)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	next, ok := nextLocalWallClockRun(spec, loc, now)
	if !ok {
		return time.Time{}, errors.New("unable to compute next local run time")
	}
	return next, nil
}

type dailyLocalRecurrenceSpec struct {
	Times []string
}

func parseDailyLocalRecurrence(raw string) (dailyLocalRecurrenceSpec, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "@daily-local") {
		return dailyLocalRecurrenceSpec{}, fmt.Errorf("expected @daily-local recurrence")
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(value, "@daily-local"))
	if remainder == "" {
		return dailyLocalRecurrenceSpec{}, errors.New("daily-local recurrence requires at least one HH:MM time")
	}
	parts := strings.Split(remainder, ",")
	times := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if _, err := time.Parse("15:04", part); err != nil {
			return dailyLocalRecurrenceSpec{}, fmt.Errorf("invalid daily-local time %q: %w", part, err)
		}
		times = append(times, part)
	}
	return dailyLocalRecurrenceSpec{Times: times}, nil
}

func nextLocalWallClockRun(spec dailyLocalRecurrenceSpec, loc *time.Location, now time.Time) (time.Time, bool) {
	localNow := now.In(loc)
	for dayOffset := 0; dayOffset < 8; dayOffset++ {
		day := localNow.AddDate(0, 0, dayOffset)
		for _, hhmm := range spec.Times {
			clock, _ := time.Parse("15:04", hhmm)
			candidate := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
			if candidate.After(localNow) {
				return candidate.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func newScheduleID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("schedule-%d", time.Now().UTC().UnixNano())
	}
	return "schedule-" + hex.EncodeToString(b[:])
}
