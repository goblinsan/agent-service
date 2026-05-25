package agent_test

import (
	"context"

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
