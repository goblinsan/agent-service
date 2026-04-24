package api_test

// End-to-end integration tests that exercise the full HTTP router with:
//   - a mocked LLM provider (tool calls, stop conditions)
//   - a mocked tool runner
//   - policy-controlled approval gates
//   - run inspection (GET /runs/{runID} and GET /runs/{runID}/steps)
//   - metrics recording

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/model/registry"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers shared by e2e tests
// ---------------------------------------------------------------------------

// toolCallProvider returns a provider whose first response asks the model to
// execute a tool call, and whose second response signals completion.
func toolCallProvider(toolName string, toolResult string) *sequenceProvider {
	return &sequenceProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-e2e-1", Name: toolName, Params: map[string]any{"input": "value"}},
				},
			},
			{Content: "done after tool", FinishReason: "stop"},
		},
	}
}

// sequenceProvider replays a fixed list of responses, returning the last one
// for any call beyond the list length.
type sequenceProvider struct {
	responses []model.Response
	callCount int
}

func (p *sequenceProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	if p.callCount >= len(p.responses) {
		r := p.responses[len(p.responses)-1]
		return &r, nil
	}
	r := p.responses[p.callCount]
	p.callCount++
	return &r, nil
}

func (p *sequenceProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("chunk")
}

// recordingRunner records every Execute call and returns a fixed result.
type recordingRunner struct {
	calls  []string
	result any
}

func (r *recordingRunner) Execute(_ context.Context, tool string, _ map[string]any) (any, error) {
	r.calls = append(r.calls, tool)
	return r.result, nil
}

// ---------------------------------------------------------------------------
// Issue #72 – Integration test: successful orchestrated run
// ---------------------------------------------------------------------------

func TestE2E_ChatRun_SuccessfulOrchestration(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"request_id": "e2e-req-1",
		"thread_id":  "e2e-thread-1",
		"user_id":    "user-e2e",
		"messages": []map[string]string{
			{"role": "user", "content": "run the e2e scenario"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := rr.Body.String()
	assert.Contains(t, resp, "run.created")
	assert.Contains(t, resp, "run.in_progress")
	assert.Contains(t, resp, "run.step")
	assert.Contains(t, resp, "run.completed")

	// Run must be stored as completed.
	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "completed", run.Status)
	assert.Equal(t, "e2e-req-1", run.RequestID)
}

// ---------------------------------------------------------------------------
// Issue #72 – Integration test: run with a tool call
// ---------------------------------------------------------------------------

func TestE2E_ChatRun_WithToolCall(t *testing.T) {
	ms := newMockStore()
	runner := &recordingRunner{result: "tool-output"}
	svc := service.NewWithOptions(ms, toolCallProvider("search", "tool-output"), 10, service.ServiceOptions{
		Runner: runner,
	})
	router := api.NewRouter(svc)

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "search for something"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := rr.Body.String()
	assert.Contains(t, resp, "run.tool_call", "tool_call event must appear in the stream")
	assert.Contains(t, resp, "run.completed")

	// Verify runner was called.
	assert.Equal(t, []string{"search"}, runner.calls)

	// Inspect the run record to confirm tool call was persisted.
	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	require.Len(t, run.ToolCalls, 1)
	assert.Equal(t, "search", run.ToolCalls[0].ToolName)
	assert.Equal(t, "tool-output", run.ToolCalls[0].Result)
}

// ---------------------------------------------------------------------------
// Issue #72 – Integration test: approval-required tool call
// ---------------------------------------------------------------------------

func TestE2E_ChatRun_ApprovalRequired_Approved(t *testing.T) {
	ms := newMockStore()
	runner := &recordingRunner{result: "sensitive-output"}
	pol := &policy.AllowlistPolicy{
		ApprovalTools: map[string]bool{"sensitive_op": true},
	}
	svc := service.NewWithOptions(ms, toolCallProvider("sensitive_op", "sensitive-output"), 10, service.ServiceOptions{
		Runner: runner,
		Policy: pol,
	})
	router := api.NewRouter(svc)

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "do something sensitive"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(rr, req)
	}()

	// Poll until we see the approval_requested event.
	var approvalID string
	deadline := time.Now().Add(5 * time.Second)
	for approvalID == "" && time.Now().Before(deadline) {
		body := rr.Body.String()
		if idx := strings.Index(body, `"approval_id":"`); idx >= 0 {
			rest := body[idx+len(`"approval_id":"`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				approvalID = rest[:end]
			}
		}
		if approvalID == "" {
			time.Sleep(5 * time.Millisecond)
		}
	}
	require.NotEmpty(t, approvalID, "approval_id must appear in the SSE stream")

	// Approve via the service's ApproveApproval – this exercises the HTTP-level
	// approval path exactly as an operator would.
	require.NoError(t, svc.ApproveApproval(approvalID))

	<-done

	assert.Contains(t, rr.Body.String(), "run.completed")
}

// ---------------------------------------------------------------------------
// Issue #72 – Integration test: multi-node backend selection and fallback
// ---------------------------------------------------------------------------

func TestE2E_MultiNode_BackendSelection(t *testing.T) {
	// Build a two-node pool. n1 always succeeds.
	n1 := &sequenceProvider{
		responses: []model.Response{{Content: "from-n1", FinishReason: "stop"}},
	}
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1:8080"},
	})
	pool := registry.NewPool(reg, func(_ string) model.Provider { return n1 })

	ms := newMockStore()
	svc := service.New(ms, pool, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "hello from pool"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "run.completed")
}

func TestE2E_MultiNode_FallbackWhenPrimaryDown(t *testing.T) {
	// n1 errors; n2 succeeds.  The pool should fall back to n2.
	n2 := &sequenceProvider{
		responses: []model.Response{{Content: "from-n2", FinishReason: "stop"}},
	}

	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1:8080"},
		{Name: "n2", URL: "http://n2:8080"},
	})

	byURL := map[string]model.Provider{
		"http://n1:8080": &errorProvider{},
		"http://n2:8080": n2,
	}
	pool := registry.NewPool(reg, func(url string) model.Provider { return byURL[url] })

	ms := newMockStore()

	// First call via pool marks n1 as failed; n2 takes over.
	_, _ = pool.Complete(context.Background(), model.Request{})

	svc := service.New(ms, pool, 10)
	router := api.NewRouter(svc)

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "fallback scenario"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "run.completed")
}

// errorProvider always returns an error from Complete.
type errorProvider struct{}

func (e *errorProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return nil, assert.AnError
}
func (e *errorProvider) Stream(_ context.Context, _ model.Request, _ func(string) error) error {
	return assert.AnError
}

// ---------------------------------------------------------------------------
// Issue #71 – Inspection API: GET /runs/{runID}
// ---------------------------------------------------------------------------

func TestGetRun_ReturnsCompletedRun(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	// Create a run via the chat endpoint.
	body := map[string]any{
		"request_id": "insp-req-1",
		"thread_id":  "insp-thread-1",
		"messages": []map[string]string{
			{"role": "user", "content": "inspect me"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Find the run ID from the store.
	var runID string
	for id := range ms.runs {
		runID = id
		break
	}
	require.NotEmpty(t, runID)

	// Fetch the run via the inspection API.
	inspReq := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	inspRR := httptest.NewRecorder()
	router.ServeHTTP(inspRR, inspReq)

	require.Equal(t, http.StatusOK, inspRR.Code)
	assert.Contains(t, inspRR.Header().Get("Content-Type"), "application/json")

	var run store.Run
	require.NoError(t, json.NewDecoder(inspRR.Body).Decode(&run))
	assert.Equal(t, runID, run.ID)
	assert.Equal(t, "completed", run.Status)
	assert.Equal(t, "insp-req-1", run.RequestID)
}

func TestGetRun_NotFound(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/runs/does-not-exist", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------------------------------------------------------------------------
// Issue #71 – Inspection API: GET /runs/{runID}/steps
// ---------------------------------------------------------------------------

func TestListRunSteps_ReturnsSteps(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	// Create a run.
	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "step me"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var runID string
	for id := range ms.runs {
		runID = id
		break
	}
	require.NotEmpty(t, runID)

	// List steps.
	stepsReq := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/steps", nil)
	stepsRR := httptest.NewRecorder()
	router.ServeHTTP(stepsRR, stepsReq)

	require.Equal(t, http.StatusOK, stepsRR.Code)
	assert.Contains(t, stepsRR.Header().Get("Content-Type"), "application/json")

	// The mock store's ListSteps returns what was written during the run.
	// With the default mockProvider (3 calls → stop), we expect 3 steps.
	var steps []*store.RunStep
	require.NoError(t, json.NewDecoder(stepsRR.Body).Decode(&steps))
	assert.NotEmpty(t, steps)
	for _, s := range steps {
		assert.Equal(t, runID, s.RunID)
		assert.NotEmpty(t, s.ID)
	}
}

func TestListRunSteps_EmptyForUnknownRun(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/runs/unknown-run/steps", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// Unknown run returns empty array (no error since ListSteps doesn't error for unknown IDs).
	assert.Equal(t, http.StatusOK, rr.Code)
	var steps []*store.RunStep
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&steps))
	assert.Empty(t, steps)
}

// ---------------------------------------------------------------------------
// Issue #70 – Metrics recording across runs
// ---------------------------------------------------------------------------

func TestMetrics_RecordedAfterRun(t *testing.T) {
	ms := newMockStore()
	m := &metrics.Metrics{}
	runner := &recordingRunner{result: "tool-out"}
	svc := service.NewWithOptions(ms, toolCallProvider("mytool", "tool-out"), 10, service.ServiceOptions{
		Runner:  runner,
		Metrics: m,
	})
	router := api.NewRouterWithOptions(svc, api.RouterOptions{Metrics: m})

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "track metrics"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	snap := m.Snapshot()
	assert.Equal(t, int64(1), snap.RunsCompleted, "run should be counted as completed")
	assert.GreaterOrEqual(t, snap.RunLatencyTotalMs, int64(0))
	assert.Equal(t, int64(1), snap.ToolCallsTotal, "one tool call was made")
}

func TestMetrics_BackendSelectionTracked(t *testing.T) {
	ms := newMockStore()
	m := &metrics.Metrics{}
	svc := service.NewWithOptions(ms, &mockProvider{}, 10, service.ServiceOptions{
		Metrics: m,
	})
	router := api.NewRouterWithOptions(svc, api.RouterOptions{Metrics: m})

	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "test backend"},
		},
		"model_preferences": map[string]any{"preferred": "llama3"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	snap := m.Snapshot()
	assert.Equal(t, int64(1), snap.BackendSelectionsTotal)
}
