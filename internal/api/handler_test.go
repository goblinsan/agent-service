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

func (m *mockStore) CreateStep(_ context.Context, step *store.RunStep) error {
	m.steps = append(m.steps, step)
	return nil
}
func (m *mockStore) ListSteps(_ context.Context, runID string) ([]*store.RunStep, error) {
	var result []*store.RunStep
	for _, s := range m.steps {
		if s.RunID == runID {
			result = append(result, s)
		}
	}
	return result, nil
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
	svc := service.New(ms, &mockProvider{}, 10)
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
	svc := service.New(ms, &mockProvider{}, 10)
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
	svc := service.New(ms, &mockProvider{}, 10)
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
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/runs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// Approval endpoints
// ---------------------------------------------------------------------------

func TestGetApproval_Found(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	// Seed a pending approval record.
	rec := svc.RequestApproval("file", map[string]any{"op": "write_file", "path": "/tmp/x"})

	req := httptest.NewRequest(http.MethodGet, "/approvals/"+rec.ID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var respBody map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&respBody))
	assert.Equal(t, rec.ID, respBody["id"])
	assert.Equal(t, "file", respBody["tool_name"])
	assert.Equal(t, "pending", respBody["status"])
}

func TestGetApproval_NotFound(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/approvals/nonexistent", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestApproveApproval(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	rec := svc.RequestApproval("git", map[string]any{"op": "git_diff"})

	req := httptest.NewRequest(http.MethodPost, "/approvals/"+rec.ID+"/approve", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)

	// Verify via GET that the status has been updated.
	req2 := httptest.NewRequest(http.MethodGet, "/approvals/"+rec.ID, nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	var respBody map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&respBody))
	assert.Equal(t, "approved", respBody["status"])
}

func TestApproveApproval_NotFound(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/approvals/no-such-id/approve", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestDenyApproval(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	rec := svc.RequestApproval("http", map[string]any{"url": "https://evil.com"})

	denyBody := `{"reason":"not in allowlist"}`
	req := httptest.NewRequest(http.MethodPost, "/approvals/"+rec.ID+"/deny",
		bytes.NewBufferString(denyBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)

	// Verify via GET that the record reflects the denial.
	req2 := httptest.NewRequest(http.MethodGet, "/approvals/"+rec.ID, nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	var respBody map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&respBody))
	assert.Equal(t, "denied", respBody["status"])
	assert.Equal(t, "not in allowlist", respBody["reason"])
}

func TestDenyApproval_NotFound(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/approvals/no-such-id/deny", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestApproveApproval_AlreadyDecided(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	rec := svc.RequestApproval("file", map[string]any{})

	// First approval should succeed.
	req := httptest.NewRequest(http.MethodPost, "/approvals/"+rec.ID+"/approve", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)

	// Second approval attempt should return 404 (already decided).
	req2 := httptest.NewRequest(http.MethodPost, "/approvals/"+rec.ID+"/approve", nil)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusNotFound, rr2.Code)
}
