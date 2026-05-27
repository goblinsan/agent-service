package api_test

import (
	"context"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

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

func (m *mockStore) UpsertUserPlan(_ context.Context, p *store.UserPlan) error {
	if m.plans[p.UserID] == nil {
		m.plans[p.UserID] = map[string]store.UserPlan{}
	}
	store.NormalizeUserPlan(p)
	m.plans[p.UserID][p.ID] = *p
	return nil
}

func (m *mockStore) ListActivePlans(_ context.Context, userID string) ([]store.UserPlan, error) {
	bucket := m.plans[userID]
	out := make([]store.UserPlan, 0, len(bucket))
	for _, p := range bucket {
		if p.Status == "done" || p.Status == "abandoned" {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (m *mockStore) GetUserPlan(_ context.Context, userID, planID string) (*store.UserPlan, error) {
	plan, ok := m.plans[userID][planID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &plan, nil
}

func (m *mockStore) DeleteUserPlan(_ context.Context, userID, planID string) error {
	if _, ok := m.plans[userID][planID]; !ok {
		return store.ErrNotFound
	}
	delete(m.plans[userID], planID)
	return nil
}

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
