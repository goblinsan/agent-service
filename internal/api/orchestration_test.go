package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// POST /internal/chat
// ---------------------------------------------------------------------------

func TestInternalChatHandler_ValidRequest(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"request_id": "req-1",
		"thread_id":  "thread-1",
		"user_id":    "user-1",
		"agent_id":   "agent-1",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/event-stream")

	responseBody := rr.Body.String()
	assert.Contains(t, responseBody, "run.created")
	assert.Contains(t, responseBody, "run.in_progress")
	assert.Contains(t, responseBody, "run.completed")
}

func TestInternalChatHandler_WithModelPreferences(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"thread_id": "thread-2",
		"messages": []map[string]string{
			{"role": "user", "content": "summarise this"},
		},
		"model_preferences": map[string]any{
			"preferred":  "gpt-4",
			"max_tokens": 512,
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	// model_selected event should be emitted when a preferred backend is set.
	assert.Contains(t, rr.Body.String(), "run.model_selected")
}

func TestInternalChatHandler_MissingMessages(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/chat",
		bytes.NewBufferString(`{"thread_id":"t1"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestInternalChatHandler_InvalidJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/chat",
		bytes.NewBufferString(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// POST /internal/automation
// ---------------------------------------------------------------------------

func TestInternalAutomationHandler_StreamMode(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"source":        "scheduler",
		"job_type":      "report",
		"workflow_id":   "wf-1",
		"prompt":        "generate weekly report",
		"response_mode": "stream",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/automation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/event-stream")

	responseBody := rr.Body.String()
	assert.Contains(t, responseBody, "run.created")
	assert.Contains(t, responseBody, "run.completed")
}

func TestInternalAutomationHandler_SyncMode(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"source":        "kulrs",
		"job_type":      "summarise",
		"prompt":        "summarise the following text",
		"response_mode": "sync",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/automation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")

	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "completed", result["status"])
	assert.NotEmpty(t, result["run_id"])
}

func TestInternalAutomationHandler_DefaultSyncMode(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	// When response_mode is omitted it defaults to sync.
	body := map[string]any{
		"source":   "worker",
		"job_type": "process",
		"prompt":   "do the work",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/automation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Response must be valid JSON with a run_id.
	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.NotEmpty(t, result["run_id"])
}

func TestInternalAutomationHandler_WithModelPreferences(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"source":        "scheduler",
		"job_type":      "report",
		"prompt":        "run the report",
		"response_mode": "stream",
		"model_preferences": map[string]any{
			"preferred": "llama3",
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/automation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "run.model_selected")
}

func TestInternalAutomationHandler_MissingSource(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/automation",
		bytes.NewBufferString(`{"job_type":"report","prompt":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestInternalAutomationHandler_MissingJobType(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/automation",
		bytes.NewBufferString(`{"source":"scheduler","prompt":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestInternalAutomationHandler_MissingPrompt(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/automation",
		bytes.NewBufferString(`{"source":"scheduler","job_type":"report"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestInternalAutomationHandler_InvalidJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/automation",
		bytes.NewBufferString(`bad-json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// Run record stores orchestration context
// ---------------------------------------------------------------------------

func TestInternalChatRun_StoresContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"request_id": "req-42",
		"thread_id":  "thread-99",
		"user_id":    "user-7",
		"agent_id":   "agent-abc",
		"messages": []map[string]string{
			{"role": string(model.RoleUser), "content": "test context"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify that the run record in the store carries the chat context.
	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "chat", run.Source)
	assert.Equal(t, "req-42", run.RequestID)
	assert.Equal(t, "thread-99", run.ThreadID)
	assert.Equal(t, "user-7", run.UserID)
	assert.Equal(t, "agent-abc", run.AgentID)
}

func TestInternalAutomationRun_StoresContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"source":      "kulrs",
		"job_type":    "ingest",
		"workflow_id": "wf-xyz",
		"prompt":      "process the batch",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/automation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Verify that the run record in the store carries the automation context.
	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "automation", run.Source)
	assert.Equal(t, "ingest", run.JobType)
	assert.Equal(t, "wf-xyz", run.WorkflowID)
}

// ---------------------------------------------------------------------------
// POST /internal/kulrs/palette
// ---------------------------------------------------------------------------

func TestKulrsPaletteHandler_ValidRequest(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"product_id": "prod-123",
		"image_urls": []string{"https://cdn.example.com/a.jpg", "https://cdn.example.com/b.jpg"},
		"workflow_id": "wf-palette-1",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")

	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "completed", result["status"])
	assert.NotEmpty(t, result["run_id"])
}

func TestKulrsPaletteHandler_MissingProductID(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette",
		bytes.NewBufferString(`{"image_urls":["https://example.com/a.jpg"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestKulrsPaletteHandler_MissingImageURLs(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette",
		bytes.NewBufferString(`{"product_id":"prod-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestKulrsPaletteHandler_InvalidJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette",
		bytes.NewBufferString(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestKulrsPaletteHandler_WithModelPreferences(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"product_id": "prod-999",
		"image_urls": []string{"https://cdn.example.com/img.jpg"},
		"model_preferences": map[string]any{
			"preferred": "llama3",
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "llama3", result["model_backend"])
}

func TestKulrsPaletteHandler_StoresAutomationContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"product_id":  "prod-555",
		"image_urls":  []string{"https://cdn.example.com/x.jpg"},
		"workflow_id": "wf-kulrs-99",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/kulrs/palette", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "automation", run.Source)
	assert.Equal(t, "palette_analysis", run.JobType)
	assert.Equal(t, "wf-kulrs-99", run.WorkflowID)
	assert.Equal(t, "completed", run.Status)
}

func TestInternalChatHandler_SSEEventTypes(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	responseBody := rr.Body.String()
	for _, eventType := range []string{"run.created", "run.in_progress", "run.step", "run.completed"} {
		assert.True(t, strings.Contains(responseBody, eventType), "expected event %q in stream", eventType)
	}
}
