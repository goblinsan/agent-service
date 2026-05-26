package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"time"

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
	return s.store.CreateNotification(ctx, n)
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
