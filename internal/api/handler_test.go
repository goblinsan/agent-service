package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	sessions map[string]*store.Session
	runs     map[string]*store.Run
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
	r, ok := m.runs[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return r, nil
}

func (m *mockStore) UpdateRun(_ context.Context, r *store.Run) error {
	m.runs[r.ID] = r
	return nil
}

func (m *mockStore) CreateStep(_ context.Context, _ *store.RunStep) error { return nil }
func (m *mockStore) ListSteps(_ context.Context, _ string) ([]*store.RunStep, error) {
	return nil, nil
}

type mockProvider struct{}

func (mp *mockProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return &model.Response{Content: "ok", FinishReason: "stop"}, nil
}

func (mp *mockProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("ok")
}

func TestCreateSession(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})
	router := api.NewRouter(svc)

	body := `{"name":"test session","description":"desc"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)

	var sess store.Session
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&sess))
	assert.Equal(t, "test session", sess.Name)
	assert.Equal(t, "desc", sess.Description)
	assert.NotEmpty(t, sess.ID)
}

func TestCreateSession_MissingName(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})
	router := api.NewRouter(svc)

	body := `{"description":"desc"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateRun(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})
	router := api.NewRouter(svc)

	sess := &store.Session{ID: "sess-1", Name: "test", CreatedAt: time.Now()}
	ms.sessions[sess.ID] = sess

	body := `{"prompt":"hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/runs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/event-stream")
}

func TestCreateRun_MissingPrompt(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{})
	router := api.NewRouter(svc)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/runs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

