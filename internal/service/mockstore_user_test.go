package service_test

import (
	"context"
	"sync"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

// In-memory user state for tests that want to verify memory injection.
type mockUserState struct {
	mu       sync.Mutex
	users    map[string]string
	memories map[string]map[string]store.UserMemory
	events   map[string][]store.UserEvent
	plans    map[string]map[string]store.UserPlan
}

var mockUsers = &mockUserState{
	users:    map[string]string{},
	memories: map[string]map[string]store.UserMemory{},
	events:   map[string][]store.UserEvent{},
	plans:    map[string]map[string]store.UserPlan{},
}

func (m *mockStore) EnsureUser(_ context.Context, id, displayName string) error {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	if _, ok := mockUsers.users[id]; !ok {
		name := displayName
		if name == "" {
			name = id
		}
		mockUsers.users[id] = name
	}
	return nil
}

func (m *mockStore) ListUserMemories(_ context.Context, userID string) ([]store.UserMemory, error) {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	bucket := mockUsers.memories[userID]
	out := make([]store.UserMemory, 0, len(bucket))
	for _, v := range bucket {
		out = append(out, v)
	}
	return out, nil
}

func (m *mockStore) UpsertUserMemory(_ context.Context, userID, key, value string, confidence float64) error {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	if mockUsers.memories[userID] == nil {
		mockUsers.memories[userID] = map[string]store.UserMemory{}
	}
	mockUsers.memories[userID][key] = store.UserMemory{Key: key, Value: value, Confidence: confidence}
	return nil
}

func (m *mockStore) AppendUserEvent(_ context.Context, evt *store.UserEvent) error {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	mockUsers.events[evt.UserID] = append(mockUsers.events[evt.UserID], *evt)
	return nil
}

func (m *mockStore) ListRecentUserEvents(_ context.Context, userID string, limit int) ([]store.UserEvent, error) {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	all := mockUsers.events[userID]
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	// Most recent first.
	out := make([]store.UserEvent, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}

func (m *mockStore) UpsertUserPlan(_ context.Context, p *store.UserPlan) error {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	if mockUsers.plans[p.UserID] == nil {
		mockUsers.plans[p.UserID] = map[string]store.UserPlan{}
	}
	mockUsers.plans[p.UserID][p.ID] = *p
	return nil
}

func (m *mockStore) ListActivePlans(_ context.Context, userID string) ([]store.UserPlan, error) {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	bucket := mockUsers.plans[userID]
	out := make([]store.UserPlan, 0, len(bucket))
	for _, v := range bucket {
		if v.Status == "done" || v.Status == "abandoned" {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (m *mockStore) GetUserPlan(_ context.Context, userID, planID string) (*store.UserPlan, error) {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	plan, ok := mockUsers.plans[userID][planID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &plan, nil
}

func (m *mockStore) DeleteUserPlan(_ context.Context, userID, planID string) error {
	mockUsers.mu.Lock()
	defer mockUsers.mu.Unlock()
	if _, ok := mockUsers.plans[userID][planID]; !ok {
		return store.ErrNotFound
	}
	delete(mockUsers.plans[userID], planID)
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
