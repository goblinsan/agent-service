package service_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	sessions map[string]*store.Session
	runs     map[string]*store.Run
	steps    []*store.RunStep
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[string]*store.Session),
		runs:     make(map[string]*store.Run),
	}
}

func (m *mockStore) CreateSession(_ context.Context, s *store.Session) error {
	m.sessions[s.ID] = s
	return nil
}

func (m *mockStore) GetSession(_ context.Context, id string) (*store.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return s, nil
}

func (m *mockStore) CreateRun(_ context.Context, r *store.Run) error {
	m.runs[r.ID] = r
	return nil
}

func (m *mockStore) GetRun(_ context.Context, id string) (*store.Run, error) {
	return m.runs[id], nil
}

func (m *mockStore) UpdateRun(_ context.Context, r *store.Run) error {
	m.runs[r.ID] = r
	return nil
}

func (m *mockStore) CreateStep(_ context.Context, step *store.RunStep) error {
	m.steps = append(m.steps, step)
	return nil
}

func (m *mockStore) ListSteps(_ context.Context, runID string) ([]*store.RunStep, error) {
	return m.steps, nil
}

type mockProvider struct {
	callCount int
}

func (mp *mockProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	mp.callCount++
	finishReason := ""
	if mp.callCount >= 3 {
		finishReason = "stop"
	}
	return &model.Response{Content: "response", FinishReason: finishReason}, nil
}

func (mp *mockProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("chunk")
}

func TestCreateSession(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})

	sess, err := svc.CreateSession(context.Background(), "my session", "a description")
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, "my session", sess.Name)
	assert.Equal(t, "a description", sess.Description)
	assert.NotZero(t, sess.CreatedAt)

	assert.Contains(t, ms.sessions, sess.ID)
}

func TestStartRun(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})

	rr := httptest.NewRecorder()
	err := svc.StartRun(context.Background(), "sess-1", "test prompt", rr)
	require.NoError(t, err)

	body := rr.Body.String()
	assert.Contains(t, body, "run.created")
	assert.Contains(t, body, "run.in_progress")
	assert.Contains(t, body, "run.step")
	assert.Contains(t, body, "run.completed")
	assert.Equal(t, 6, strings.Count(body, "data: "), "expected 6 SSE events: created, in_progress, 3 steps, completed")
}

func TestStartRun_RunPersistedAsCompleted(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})

	rr := httptest.NewRecorder()
	err := svc.StartRun(context.Background(), "sess-1", "hello", rr)
	require.NoError(t, err)

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "completed", run.Status)
	assert.NotEmpty(t, run.Response)
}

