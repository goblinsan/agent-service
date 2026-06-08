package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

type fakeScheduleStore struct {
	created *store.ScheduledJob
	listed  []store.ScheduledJob
}

func (f *fakeScheduleStore) CreateScheduledJob(_ context.Context, job *store.ScheduledJob) error {
	copy := *job
	f.created = &copy
	return nil
}

func (f *fakeScheduleStore) ListScheduledJobs(_ context.Context, _ string, _ int) ([]store.ScheduledJob, error) {
	return f.listed, nil
}

func (f *fakeScheduleStore) ListScheduledJobHistory(_ context.Context, _ string, _ int) ([]store.ScheduledJob, error) {
	return f.listed, nil
}

func TestScheduleCreateToolUsesDelayAndRunContextDefaults(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ScheduleCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")
	ctx = WithRunMetadata(ctx, "thread-1", "agent-1")

	before := time.Now().UTC()
	out, err := tool.Execute(ctx, map[string]any{
		"prompt":        "Remind me to breathe.",
		"delay_seconds": 60,
	})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.created == nil {
		t.Fatal("expected schedule to be created")
	}
	if st.created.UserID != "alice" || st.created.ThreadID != "thread-1" || st.created.AgentID != "agent-1" {
		t.Fatalf("unexpected created job: %+v", st.created)
	}
	if st.created.RunAt.Before(before.Add(59*time.Second)) || st.created.RunAt.After(after.Add(61*time.Second)) {
		t.Fatalf("unexpected run_at: %s", st.created.RunAt)
	}
	result, ok := out.(map[string]any)
	if !ok || result["status"] != "ok" {
		t.Fatalf("unexpected tool result: %#v", out)
	}
}

func TestScheduleCreateToolAcceptsAbsoluteRunAt(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ScheduleCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")

	runAt := "2026-05-26T16:30:00Z"
	_, err := tool.Execute(ctx, map[string]any{
		"prompt": "Check the laundry.",
		"run_at": runAt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.created == nil {
		t.Fatal("expected schedule to be created")
	}
	if got := st.created.RunAt.Format(time.RFC3339); got != runAt {
		t.Fatalf("expected run_at %s, got %s", runAt, got)
	}
}

func TestScheduleCreateToolRequiresTiming(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ScheduleCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")

	_, err := tool.Execute(ctx, map[string]any{"prompt": "Hello"})
	if err == nil {
		t.Fatal("expected error when no timing supplied")
	}
}

func TestProjectCheckinCreateToolBuildsDailyLocalSchedule(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ProjectCheckinCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")
	ctx = WithRunMetadata(ctx, "thread-9", "agent-9")

	out, err := tool.Execute(ctx, map[string]any{
		"windows":  []any{"morning", "night"},
		"timezone": "America/New_York",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.created == nil {
		t.Fatal("expected schedule to be created")
	}
	if st.created.Kind != "project_checkin" {
		t.Fatalf("expected project_checkin kind, got %q", st.created.Kind)
	}
	if st.created.Recurrence != "@daily-local 08:00,20:00" {
		t.Fatalf("unexpected recurrence: %q", st.created.Recurrence)
	}
	if st.created.Timezone != "America/New_York" {
		t.Fatalf("unexpected timezone: %q", st.created.Timezone)
	}
	if st.created.ThreadID != "thread-9" || st.created.AgentID != "agent-9" {
		t.Fatalf("unexpected defaults: %+v", st.created)
	}
	result, ok := out.(map[string]any)
	if !ok || result["status"] != "ok" {
		t.Fatalf("unexpected tool result: %#v", out)
	}
}

func TestProjectCheckinCreateToolRejectsUnknownWindow(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ProjectCheckinCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")

	_, err := tool.Execute(ctx, map[string]any{
		"windows": []any{"brunch"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported window")
	}
}

func TestDefaultProjectCheckinPromptReferencesPlanProgressUpdate(t *testing.T) {
	for _, windows := range [][]string{
		{"morning"},
		{"morning", "night"},
		{"afternoon"},
		{},
	} {
		prompt := defaultProjectCheckinPrompt(windows)
		if strings.Contains(prompt, "plan_upsert") {
			t.Errorf("prompt for windows %v still references plan_upsert: %q", windows, prompt)
		}
		if !strings.Contains(prompt, "plan_progress_update") {
			t.Errorf("prompt for windows %v does not reference plan_progress_update: %q", windows, prompt)
		}
	}
}

func TestProjectCheckinCreateToolEmbedsSafePrompt(t *testing.T) {
	st := &fakeScheduleStore{}
	tool := &ProjectCheckinCreateTool{Store: st}
	ctx := WithUserID(context.Background(), "alice")

	_, err := tool.Execute(ctx, map[string]any{
		"windows":  []any{"morning"},
		"timezone": "America/New_York",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.created == nil {
		t.Fatal("expected schedule to be created")
	}
	if strings.Contains(st.created.Prompt, "plan_upsert") {
		t.Errorf("stored prompt still references plan_upsert: %q", st.created.Prompt)
	}
	if !strings.Contains(st.created.Prompt, "plan_progress_update") {
		t.Errorf("stored prompt does not reference plan_progress_update: %q", st.created.Prompt)
	}
}
