package agent_test

import (
	"context"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

// User/memory/plan stubs so mockStore satisfies the Store interface. Tests in
// this package don't exercise per-user persistence; behaviour is a quiet no-op.

func (m *mockStore) EnsureUser(_ context.Context, _, _ string) error { return nil }

func (m *mockStore) ListUserMemories(_ context.Context, _ string) ([]store.UserMemory, error) {
	return nil, nil
}

func (m *mockStore) UpsertUserMemory(_ context.Context, _, _, _ string, _ float64) error {
	return nil
}

func (m *mockStore) AppendUserEvent(_ context.Context, _ *store.UserEvent) error { return nil }

func (m *mockStore) ListRecentUserEvents(_ context.Context, _ string, _ int) ([]store.UserEvent, error) {
	return nil, nil
}

func (m *mockStore) UpsertUserPlan(_ context.Context, _ *store.UserPlan) error { return nil }

func (m *mockStore) ListActivePlans(_ context.Context, _ string) ([]store.UserPlan, error) {
	return nil, nil
}

func (m *mockStore) GetUserPlan(_ context.Context, _, _ string) (*store.UserPlan, error) {
	return nil, store.ErrNotFound
}

func (m *mockStore) DeleteUserPlan(_ context.Context, _, _ string) error { return store.ErrNotFound }

func (m *mockStore) CreateNotification(_ context.Context, _ *store.Notification) error { return nil }
func (m *mockStore) ListNotifications(_ context.Context, _ string, _ bool, _ int) ([]store.Notification, error) {
	return nil, nil
}
func (m *mockStore) MarkNotificationRead(_ context.Context, _, _ string) error  { return nil }
func (m *mockStore) MarkAllNotificationsRead(_ context.Context, _ string) error { return nil }
func (m *mockStore) DeleteNotification(_ context.Context, _, _ string) error    { return nil }

func (m *mockStore) CreateScheduledJob(_ context.Context, _ *store.ScheduledJob) error { return nil }
func (m *mockStore) ListScheduledJobs(_ context.Context, _ string, _ int) ([]store.ScheduledJob, error) {
	return nil, nil
}
func (m *mockStore) ListScheduledJobHistory(_ context.Context, _ string, _ int) ([]store.ScheduledJob, error) {
	return nil, nil
}
func (m *mockStore) DeleteScheduledJob(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) AcquireDueScheduledJobs(_ context.Context, _ int, _ time.Duration) ([]store.ScheduledJob, error) {
	return nil, nil
}
func (m *mockStore) MarkScheduledJobResult(_ context.Context, _, _ string, _ time.Time, _ *time.Time) error {
	return nil
}

func (m *mockStore) UpsertDeviceToken(_ context.Context, _ *store.DeviceToken) error { return nil }
func (m *mockStore) DeleteDeviceToken(_ context.Context, _, _ string) error          { return nil }
func (m *mockStore) ListDeviceTokens(_ context.Context, _, _ string) ([]store.DeviceToken, error) {
	return nil, nil
}
