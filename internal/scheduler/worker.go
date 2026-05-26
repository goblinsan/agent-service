package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
)

// WorkerConfig controls polling and lease behavior for scheduled jobs.
type WorkerConfig struct {
	PollInterval time.Duration
	Lease        time.Duration
	BatchSize    int
}

// Worker polls due scheduled jobs, runs them, and emits notifications.
type Worker struct {
	store   store.Store
	service *service.Service
	cfg     WorkerConfig
}

func New(st store.Store, svc *service.Service, cfg WorkerConfig) *Worker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.Lease <= 0 {
		cfg.Lease = 45 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	return &Worker{store: st, service: svc, cfg: cfg}
}

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	slog.Info("scheduler worker started", "poll_interval", w.cfg.PollInterval.String(), "batch_size", w.cfg.BatchSize)
	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler worker stopped")
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	jobs, err := w.store.AcquireDueScheduledJobs(ctx, w.cfg.BatchSize, w.cfg.Lease)
	if err != nil {
		slog.Error("scheduler acquire failed", "error", err)
		return
	}
	for _, job := range jobs {
		w.runJob(ctx, job)
	}
}

func (w *Worker) runJob(ctx context.Context, job store.ScheduledJob) {
	now := time.Now().UTC()
	result, err := w.service.RunScheduledJob(ctx, job)
	if err != nil {
		slog.Error("scheduled job failed", "job_id", job.ID, "user_id", job.UserID, "error", err)
		_ = w.emitNotification(ctx, store.Notification{
			ID:        newID(),
			UserID:    job.UserID,
			Kind:      "scheduled_job.failed",
			Title:     "Scheduled job failed",
			Body:      trimMessage(err.Error(), 280),
			ThreadID:  job.ThreadID,
				Payload:   map[string]any{"scheduled_job_id": job.ID},
				CreatedAt: now,
			})
		status, nextRun := nextJobState(job, now, false)
		_ = w.store.MarkScheduledJobResult(ctx, job.ID, status, now, nextRun)
		return
	}

	body := trimMessage(result.Output, 280)
	if body == "" {
		body = "Scheduled work completed."
	}
	_ = w.emitNotification(ctx, store.Notification{
		ID:          newID(),
		UserID:      job.UserID,
		Kind:        "scheduled_job.completed",
		Title:       "Scheduled work completed",
		Body:        body,
		ThreadID:    job.ThreadID,
		SourceRunID: result.RunID,
		Payload: map[string]any{
			"scheduled_job_id": job.ID,
			"run_id":           result.RunID,
			"status":           result.Status,
			},
			CreatedAt: now,
		})
	status, nextRun := nextJobState(job, now, true)
	_ = w.store.MarkScheduledJobResult(ctx, job.ID, status, now, nextRun)
}

func (w *Worker) emitNotification(ctx context.Context, n store.Notification) error {
	if err := w.service.CreateNotification(ctx, &n); err != nil {
		slog.Error("notification create failed", "user_id", n.UserID, "error", err)
		return err
	}
	return nil
}

func nextJobState(job store.ScheduledJob, now time.Time, succeeded bool) (string, *time.Time) {
	recurrence := strings.TrimSpace(job.Recurrence)
	if recurrence == "" {
		if succeeded {
			return "completed", nil
		}
		return "failed", nil
	}
	next, err := nextRunAt(job, now)
	if err != nil {
		slog.Warn("scheduled job recurrence invalid; disabling job", "job_id", job.ID, "recurrence", recurrence, "error", err)
		return "failed", nil
	}
	return "pending", next
}

func nextRunAt(job store.ScheduledJob, now time.Time) (*time.Time, error) {
	recurrence := strings.TrimSpace(job.Recurrence)
	if recurrence == "" {
		return nil, nil
	}
	d, err := parseRecurrence(recurrence)
	if err != nil {
		return nil, err
	}
	next := job.RunAt
	if next.IsZero() {
		next = now
	}
	for !next.After(now) {
		next = next.Add(d)
	}
	return &next, nil
}

func parseRecurrence(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "@every"))
	d, err := time.ParseDuration(strings.TrimSpace(trimmed))
	if err != nil {
		return 0, fmt.Errorf("expected @every <duration> or <duration>: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return d, nil
}

func trimMessage(s string, max int) string {
	t := strings.TrimSpace(s)
	if max <= 0 || len(t) <= max {
		return t
	}
	if max < 4 {
		return t[:max]
	}
	return t[:max-3] + "..."
}

func newID() string {
	return fmt.Sprintf("sched-%d", time.Now().UTC().UnixNano())
}
