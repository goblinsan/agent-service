package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunScheduledJobReminderBypassesModelAndNormalizesBody(t *testing.T) {
	ms := newMockStore()
	provider := &mockProvider{}
	svc := service.New(ms, provider, 10)

	result, err := svc.RunScheduledJob(context.Background(), store.ScheduledJob{
		ID:       "schedule-1",
		UserID:   "jamescoghlan",
		Kind:     "reminder",
		Prompt:   "remind me to take a bath",
		ThreadID: "thread-1",
		AgentID:  "gateway",
		RunAt:    time.Date(2026, 5, 26, 18, 30, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 0, provider.callCount, "reminder jobs should not invoke the model")
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, "Remember to take a bath.", result.Output)
	require.NotEmpty(t, result.RunID)

	run := ms.runs[result.RunID]
	require.NotNil(t, run)
	assert.Equal(t, "completed", run.Status)
	assert.Equal(t, "Remember to take a bath.", run.Response)
	assert.Equal(t, "thread-1", run.ThreadID)
	assert.Equal(t, "jamescoghlan", run.UserID)
	assert.Equal(t, "gateway", run.AgentID)
	assert.Equal(t, "reminder", run.JobType)
}
