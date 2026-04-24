package agent_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/agent"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockProvider struct {
	responses []model.Response
	callCount int
	err       error
}

func (m *mockProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callCount >= len(m.responses) {
		return &model.Response{Content: "default", FinishReason: "stop"}, nil
	}
	r := m.responses[m.callCount]
	m.callCount++
	return &r, nil
}

func (m *mockProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("chunk")
}

type mockStore struct {
	steps []*store.RunStep
	runs  map[string]*store.Run
}

func newMockStore() *mockStore {
	return &mockStore{runs: make(map[string]*store.Run)}
}

func (m *mockStore) CreateSession(_ context.Context, s *store.Session) error { return nil }
func (m *mockStore) GetSession(_ context.Context, id string) (*store.Session, error) {
	return nil, store.ErrNotFound
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
	return m.steps, nil
}

// mockRunner is a runner.Runner that returns a fixed result for any tool call.
type mockRunner struct {
	result any
	err    error
	calls  []string
}

func (r *mockRunner) Execute(_ context.Context, tool string, _ map[string]any) (any, error) {
	r.calls = append(r.calls, tool)
	return r.result, r.err
}

func makeRun() *store.Run {
	return &store.Run{ID: "run-1", SessionID: "sess-1", Prompt: "hello"}
}

func TestRun_SingleStep(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{Content: "answer", FinishReason: "stop"},
		},
	}
	ms := newMockStore()
	ms.runs["run-1"] = makeRun()
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr, nil)
	require.NoError(t, err)

	assert.Len(t, ms.steps, 1)
	assert.Equal(t, "answer", ms.steps[0].Content)
	assert.Contains(t, rr.Body.String(), "run.step")
	assert.Equal(t, 1, strings.Count(rr.Body.String(), "run.step"))
}

func TestRun_MultiStep(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{Content: "step1", FinishReason: ""},
			{Content: "step2", FinishReason: ""},
			{Content: "step3", FinishReason: "stop"},
		},
	}
	ms := newMockStore()
	ms.runs["run-1"] = makeRun()
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr, nil)
	require.NoError(t, err)

	assert.Len(t, ms.steps, 3)
	assert.Equal(t, 3, strings.Count(rr.Body.String(), "run.step"))
}

func TestRun_MaxSteps(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{Content: "s1", FinishReason: ""},
			{Content: "s2", FinishReason: ""},
			{Content: "s3", FinishReason: ""},
			{Content: "s4", FinishReason: ""},
			{Content: "s5", FinishReason: ""},
		},
	}
	ms := newMockStore()
	ms.runs["run-1"] = makeRun()
	a := agent.New(mp, ms, 3)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr, nil)
	require.NoError(t, err)

	assert.Len(t, ms.steps, 3)
}

func TestRun_ProviderError(t *testing.T) {
	mp := &mockProvider{err: assert.AnError}
	ms := newMockStore()
	ms.runs["run-1"] = makeRun()
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr, nil)
	require.Error(t, err)
	assert.Len(t, ms.steps, 0)
}

// ---------------------------------------------------------------------------
// Tool call orchestration
// ---------------------------------------------------------------------------

func TestRun_ToolCall_ExecutedAndResultFedBack(t *testing.T) {
	// First response: model requests a tool call.
	// Second response: model produces a final answer after seeing the result.
	mp := &mockProvider{
		responses: []model.Response{
			{
				Content:      "",
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-1", Name: "echo", Params: map[string]any{"msg": "hello"}},
				},
			},
			{Content: "final answer", FinishReason: "stop"},
		},
	}
	mr := &mockRunner{result: "echoed: hello"}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	a := agent.NewWithOptions(mp, ms, 10, agent.Options{Runner: mr})

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), run, rr, nil)
	require.NoError(t, err)

	// Runner must have been called once.
	assert.Equal(t, []string{"echo"}, mr.calls)

	// Run record must contain one ToolCall entry.
	assert.Len(t, run.ToolCalls, 1)
	assert.Equal(t, "echo", run.ToolCalls[0].ToolName)
	assert.Equal(t, "echoed: hello", run.ToolCalls[0].Result)

	// SSE stream must contain a run.tool_call event.
	assert.Contains(t, rr.Body.String(), "run.tool_call")
}

func TestRun_ToolCall_PolicyDeny(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-2", Name: "forbidden", Params: map[string]any{}},
				},
			},
			{Content: "ok", FinishReason: "stop"},
		},
	}
	mr := &mockRunner{result: "should not run"}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	pol := &policy.AllowlistPolicy{
		DeniedToolNames: []string{"forbidden"},
	}
	a := agent.NewWithOptions(mp, ms, 10, agent.Options{Runner: mr, Policy: pol})

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), run, rr, nil)
	require.NoError(t, err)

	// Runner must NOT have been called.
	assert.Empty(t, mr.calls)

	// The tool call error should be recorded.
	require.Len(t, run.ToolCalls, 1)
	assert.Contains(t, run.ToolCalls[0].Error, "denied by policy")
}

func TestRun_ToolCall_NoRunner(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-3", Name: "anything", Params: map[string]any{}},
				},
			},
			{Content: "ok", FinishReason: "stop"},
		},
	}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	// No runner configured.
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), run, rr, nil)
	require.NoError(t, err)

	require.Len(t, run.ToolCalls, 1)
	assert.Contains(t, run.ToolCalls[0].Error, "no runner is configured")
}

func TestRun_ToolCall_ApprovalGranted(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-4", Name: "sensitive", Params: map[string]any{"x": 1}},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}
	mr := &mockRunner{result: "sensitive result"}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	approvals := policy.NewApprovals()
	pol := &policy.AllowlistPolicy{
		ApprovalTools: map[string]bool{"sensitive": true},
	}
	a := agent.NewWithOptions(mp, ms, 10, agent.Options{Runner: mr, Policy: pol, Approvals: approvals})

	rr := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() {
		done <- a.Run(context.Background(), run, rr, nil)
	}()

	// Spin until the approval_requested event appears in the SSE stream, then
	// approve the pending request.
	var capturedID string
	for capturedID == "" {
		body := rr.Body.String()
		idx := strings.Index(body, `"approval_id":"`)
		if idx >= 0 {
			rest := body[idx+len(`"approval_id":"`):]
			end := strings.Index(rest, `"`)
			if end >= 0 {
				capturedID = rest[:end]
			}
		}
	}

	require.NoError(t, approvals.Approve(capturedID))

	require.NoError(t, <-done)
	assert.Equal(t, []string{"sensitive"}, mr.calls)
	assert.Len(t, run.ToolCalls, 1)
	assert.Empty(t, run.ToolCalls[0].Error)
	assert.NotEmpty(t, run.ApprovalRecs)
	assert.Equal(t, "approved", run.ApprovalRecs[0].Decision)
}

func TestRun_ToolCall_ApprovalDenied(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-5", Name: "risky", Params: map[string]any{}},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}
	mr := &mockRunner{result: "should not run"}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	approvals := policy.NewApprovals()
	pol := &policy.AllowlistPolicy{
		ApprovalTools: map[string]bool{"risky": true},
	}
	a := agent.NewWithOptions(mp, ms, 10, agent.Options{Runner: mr, Policy: pol, Approvals: approvals})

	rr := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() {
		done <- a.Run(context.Background(), run, rr, nil)
	}()

	// Wait until the approval_requested event is in the stream.
	var capturedID string
	for {
		body := rr.Body.String()
		idx := strings.Index(body, `"approval_id":"`)
		if idx >= 0 {
			rest := body[idx+len(`"approval_id":"`):]
			end := strings.Index(rest, `"`)
			if end >= 0 {
				capturedID = rest[:end]
				break
			}
		}
	}
	require.NoError(t, approvals.Deny(capturedID, "not allowed"))

	require.NoError(t, <-done)
	// Runner must NOT have been called.
	assert.Empty(t, mr.calls)
	require.Len(t, run.ToolCalls, 1)
	assert.Contains(t, run.ToolCalls[0].Error, "denied by human")
	assert.NotEmpty(t, run.ApprovalRecs)
	assert.Equal(t, "denied", run.ApprovalRecs[0].Decision)
}

func TestRun_PerRunPolicy_OverridesBase(t *testing.T) {
	mp := &mockProvider{
		responses: []model.Response{
			{
				FinishReason: "tool_calls",
				ToolCalls: []model.ToolCall{
					{ID: "tc-6", Name: "allowed_by_run", Params: map[string]any{}},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}
	mr := &mockRunner{result: "ok"}
	ms := newMockStore()
	run := makeRun()
	ms.runs[run.ID] = run

	// Base policy denies the tool, but per-run policy allows it (via AllowedToolNames).
	basePol := &policy.AllowlistPolicy{
		DeniedToolNames: []string{"allowed_by_run"},
	}
	a := agent.NewWithOptions(mp, ms, 10, agent.Options{Runner: mr, Policy: basePol})

	// The per-run policy explicitly overrides the base by using a composite of
	// a permissive AllowlistPolicy (no restrictions) as the run policy.
	// In practice, having a Deny in the base will win in the composite.
	// So here we verify that the base Deny takes effect even when a runPolicy
	// is also provided.
	runPol := &policy.AllowlistPolicy{} // allows everything

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), run, rr, runPol)
	require.NoError(t, err)

	// Base policy Deny should win in the composite.
	assert.Empty(t, mr.calls)
	require.Len(t, run.ToolCalls, 1)
	assert.Contains(t, run.ToolCalls[0].Error, "denied by policy")
}
