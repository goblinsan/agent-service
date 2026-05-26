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

// ScheduleCreateTool creates a scheduled job for the current authenticated user.
// It is intended for reminders, follow-ups, and delayed agent work.
type ScheduleCreateTool struct {
	Store scheduleStore
}

func (t *ScheduleCreateTool) Definition() Tool {
	return Tool{
		Name: "create_schedule",
		Description: "Create a reminder or delayed follow-up for the current user. Use this when the user asks to be reminded later or asks you to do something at a future time. Prefer delay_seconds for relative requests like 'in 1 minute'. If you know the exact wall-clock time, pass run_at as an RFC3339 timestamp. The current thread and agent are reused automatically unless you override them.",
		Params: []Param{
			{Name: "prompt", Type: "string", Description: "The prompt that should run at the scheduled time, phrased as the future reminder or task itself.", Required: true},
			{Name: "delay_seconds", Type: "int", Description: "Relative delay in seconds from now. Prefer this for requests like 'in 1 minute'.", Required: false},
			{Name: "run_at", Type: "string", Description: "Absolute execution time as an RFC3339 timestamp. Use this instead of delay_seconds when the exact time is known.", Required: false},
			{Name: "recurrence", Type: "string", Description: "Optional recurrence, for example '@every 24h'. Leave empty for one-shot reminders.", Required: false},
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

	job := &store.ScheduledJob{
		ID:         newScheduleID(),
		UserID:     userID,
		Kind:       "reminder",
		Prompt:     prompt,
		ThreadID:   threadID,
		AgentID:    agentID,
		RunAt:      runAt.UTC(),
		Recurrence: recurrence,
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

func newScheduleID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("schedule-%d", time.Now().UTC().UnixNano())
	}
	return "schedule-" + hex.EncodeToString(b[:])
}
