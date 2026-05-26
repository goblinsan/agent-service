package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/goblinsan/agent-service/internal/store"
)

func (s *Service) CreateScheduledJob(ctx context.Context, job *store.ScheduledJob) error {
	return s.store.CreateScheduledJob(ctx, job)
}

func (s *Service) ListScheduledJobs(ctx context.Context, userID string, limit int) ([]store.ScheduledJob, error) {
	return s.store.ListScheduledJobs(ctx, userID, limit)
}

func (s *Service) DeleteScheduledJob(ctx context.Context, userID, jobID string) error {
	return s.store.DeleteScheduledJob(ctx, userID, jobID)
}

func (s *Service) ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]store.Notification, error) {
	return s.store.ListNotifications(ctx, userID, unreadOnly, limit)
}

func (s *Service) MarkNotificationRead(ctx context.Context, userID, notificationID string) error {
	return s.store.MarkNotificationRead(ctx, userID, notificationID)
}

func (s *Service) MarkAllNotificationsRead(ctx context.Context, userID string) error {
	return s.store.MarkAllNotificationsRead(ctx, userID)
}

func (s *Service) DeleteNotification(ctx context.Context, userID, notificationID string) error {
	return s.store.DeleteNotification(ctx, userID, notificationID)
}

func (s *Service) CreateNotification(ctx context.Context, n *store.Notification) error {
	if err := s.store.CreateNotification(ctx, n); err != nil {
		return err
	}
	if s.notifier != nil {
		if err := s.notifier.DispatchNotification(ctx, *n); err != nil {
			slog.Warn("notification fanout failed", "user_id", n.UserID, "notification_id", n.ID, "error", err)
		}
	}
	return nil
}

func (s *Service) UpsertDeviceToken(ctx context.Context, token *store.DeviceToken) error {
	return s.store.UpsertDeviceToken(ctx, token)
}

func (s *Service) DeleteDeviceToken(ctx context.Context, userID, token string) error {
	return s.store.DeleteDeviceToken(ctx, userID, token)
}

func (s *Service) ListDeviceTokens(ctx context.Context, userID, platform string) ([]store.DeviceToken, error) {
	return s.store.ListDeviceTokens(ctx, userID, platform)
}

// RunScheduledJob executes a scheduled prompt as a sync automation run and
// returns the parsed automation result body.
func (s *Service) RunScheduledJob(ctx context.Context, job store.ScheduledJob) (*AutomationRunResult, error) {
	if job.UserID == "" {
		return nil, errors.New("scheduled job user_id is required")
	}
	if strings.EqualFold(strings.TrimSpace(job.Kind), "reminder") {
		return s.runReminderJob(ctx, job)
	}
	req := &AutomationRunRequest{
		RequestID:    fmt.Sprintf("schedule:%s:%d", job.ID, time.Now().UTC().Unix()),
		Source:       "scheduler",
		JobType:      firstNonEmpty(job.Kind, "scheduled_job"),
		WorkflowID:   fmt.Sprintf("scheduled:%s", job.ID),
		ThreadID:     job.ThreadID,
		UserID:       job.UserID,
		AgentID:      job.AgentID,
		Prompt:       job.Prompt,
		ResponseMode: "sync",
		Metadata: map[string]any{
			"scheduled_job_id": job.ID,
			"run_at":           job.RunAt.Format(time.RFC3339),
		},
	}
	if job.Payload != nil {
		req.Context = job.Payload
	}

	rr := httptest.NewRecorder()
	if err := s.StartAutomationRun(ctx, req, rr); err != nil {
		return nil, err
	}
	if rr.Code >= 400 {
		return nil, fmt.Errorf("automation sync run failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result AutomationRunResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) runReminderJob(ctx context.Context, job store.ScheduledJob) (*AutomationRunResult, error) {
	now := time.Now().UTC()
	body := buildReminderBody(job.Prompt)
	run := &store.Run{
		ID:         newID(),
		SessionID:  job.ThreadID,
		Source:     string(store.RunSourceAutomation),
		Prompt:     job.Prompt,
		Status:     "completed",
		Response:   body,
		RequestID:  fmt.Sprintf("schedule:%s:%d", job.ID, now.Unix()),
		ThreadID:   job.ThreadID,
		UserID:     job.UserID,
		AgentID:    job.AgentID,
		WorkflowID: fmt.Sprintf("scheduled:%s", job.ID),
		JobType:    firstNonEmpty(job.Kind, "scheduled_job"),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create reminder run: %w", err)
	}
	return &AutomationRunResult{
		RunID:  run.ID,
		Status: "completed",
		Output: body,
	}, nil
}

func buildReminderBody(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return "Reminder."
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "remind me to "):
		trimmed = "Remember to " + strings.TrimSpace(trimmed[len("remind me to "):])
	case strings.HasPrefix(lower, "remind me "):
		trimmed = "Reminder: " + strings.TrimSpace(trimmed[len("remind me "):])
	}
	trimmed = sentenceCase(trimmed)
	if !strings.HasSuffix(trimmed, ".") && !strings.HasSuffix(trimmed, "!") && !strings.HasSuffix(trimmed, "?") {
		trimmed += "."
	}
	return trimmed
}

func sentenceCase(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
