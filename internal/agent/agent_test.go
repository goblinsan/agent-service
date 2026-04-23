package agent_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/agent"
	"github.com/goblinsan/agent-service/internal/model"
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
}

func (m *mockStore) CreateSession(_ context.Context, s *store.Session) error { return nil }
func (m *mockStore) GetSession(_ context.Context, id string) (*store.Session, error) {
	return nil, store.ErrNotFound
}
func (m *mockStore) CreateRun(_ context.Context, r *store.Run) error    { return nil }
func (m *mockStore) GetRun(_ context.Context, id string) (*store.Run, error) { return nil, nil }
func (m *mockStore) UpdateRun(_ context.Context, r *store.Run) error    { return nil }
func (m *mockStore) CreateStep(_ context.Context, step *store.RunStep) error {
	m.steps = append(m.steps, step)
	return nil
}
func (m *mockStore) ListSteps(_ context.Context, runID string) ([]*store.RunStep, error) {
	return m.steps, nil
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
	ms := &mockStore{}
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr)
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
	ms := &mockStore{}
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr)
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
	ms := &mockStore{}
	a := agent.New(mp, ms, 3)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr)
	require.NoError(t, err)

	assert.Len(t, ms.steps, 3)
}

func TestRun_ProviderError(t *testing.T) {
	mp := &mockProvider{err: assert.AnError}
	ms := &mockStore{}
	a := agent.New(mp, ms, 10)

	rr := httptest.NewRecorder()
	err := a.Run(context.Background(), makeRun(), rr)
	require.Error(t, err)
	assert.Len(t, ms.steps, 0)
}
