package scheduler

import (
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

func TestNextJobStateOneShot(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	job := store.ScheduledJob{ID: "job-1"}

	status, nextRun := nextJobState(job, now, true)
	if status != "completed" {
		t.Fatalf("expected completed status, got %q", status)
	}
	if nextRun != nil {
		t.Fatalf("expected nil nextRun for completed one-shot job")
	}

	status, nextRun = nextJobState(job, now, false)
	if status != "failed" {
		t.Fatalf("expected failed status, got %q", status)
	}
	if nextRun != nil {
		t.Fatalf("expected nil nextRun for failed one-shot job")
	}
}

func TestNextJobStateRecurring(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	job := store.ScheduledJob{
		ID:         "job-2",
		RunAt:      now.Add(-2 * time.Hour),
		Recurrence: "@every 1h",
	}

	status, nextRun := nextJobState(job, now, true)
	if status != "pending" {
		t.Fatalf("expected pending status, got %q", status)
	}
	if nextRun == nil {
		t.Fatal("expected next run for recurring job")
	}
	if !nextRun.After(now) {
		t.Fatalf("expected nextRun after now, got %s", nextRun)
	}
}

func TestNextJobStateInvalidRecurrenceDisablesJob(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	job := store.ScheduledJob{
		ID:         "job-3",
		RunAt:      now.Add(-1 * time.Hour),
		Recurrence: "@every nope",
	}

	status, nextRun := nextJobState(job, now, false)
	if status != "failed" {
		t.Fatalf("expected failed status, got %q", status)
	}
	if nextRun != nil {
		t.Fatalf("expected nil nextRun for invalid recurrence, got %s", nextRun)
	}
}
