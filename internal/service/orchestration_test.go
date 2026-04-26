package service_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type usageProvider struct{}

func (p *usageProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return &model.Response{
		Content:      "usage-aware response",
		FinishReason: "stop",
		Usage: model.Usage{
			PromptTokens:     14,
			CompletionTokens: 6,
			TotalTokens:      20,
		},
	}, nil
}

func (p *usageProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk("usage-aware response")
}

type jsonProvider struct{}

func (p *jsonProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return &model.Response{
		Content:      `{"name":"Voltage Mirage","description":"Electric dusk tones.","colors":["#112233","#445566","#778899"]}`,
		FinishReason: "stop",
	}, nil
}

func (p *jsonProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk(`{"name":"Voltage Mirage","description":"Electric dusk tones.","colors":["#112233","#445566","#778899"]}`)
}

type kulrsAnalysisProvider struct{}

func (p *kulrsAnalysisProvider) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return &model.Response{
		Content:      `{"dominant_colors":["#112233","#223344"],"accent_colors":["#445566"],"description":"Moody blue-gold balance."}`,
		FinishReason: "stop",
	}, nil
}

func (p *kulrsAnalysisProvider) Stream(_ context.Context, _ model.Request, onChunk func(string) error) error {
	return onChunk(`{"dominant_colors":["#112233","#223344"],"accent_colors":["#445566"],"description":"Moody blue-gold balance."}`)
}

// ---------------------------------------------------------------------------
// StartChatRun
// ---------------------------------------------------------------------------

func TestStartChatRun_StreamsExpectedEvents(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.ChatRunRequest{
		RequestID: "req-1",
		ThreadID:  "thread-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello"},
		},
	}

	rr := httptest.NewRecorder()
	err := svc.StartChatRun(context.Background(), req, rr)
	require.NoError(t, err)

	body := rr.Body.String()
	assert.Contains(t, body, "run.created")
	assert.Contains(t, body, "run.in_progress")
	assert.Contains(t, body, "run.step")
	assert.Contains(t, body, "run.completed")
}

func TestStartChatRun_EmitsModelSelectedWhenPreferenceSet(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.ChatRunRequest{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		ModelPreferences: &service.ModelPreferences{
			Preferred: "gpt-4",
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartChatRun(context.Background(), req, rr))

	body := rr.Body.String()
	assert.Contains(t, body, "run.model_selected")
	assert.Contains(t, body, "gpt-4")
}

func TestStartChatRun_NoModelSelectedEventWhenNoPreference(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.ChatRunRequest{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartChatRun(context.Background(), req, rr))

	assert.NotContains(t, rr.Body.String(), "run.model_selected")
}

func TestStartChatRun_PersistsRunWithChatContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.ChatRunRequest{
		RequestID: "req-99",
		ThreadID:  "thread-42",
		UserID:    "user-7",
		AgentID:   "agent-abc",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello world"},
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartChatRun(context.Background(), req, rr))

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "chat", run.Source)
	assert.Equal(t, "req-99", run.RequestID)
	assert.Equal(t, "thread-42", run.ThreadID)
	assert.Equal(t, "thread-42", run.SessionID) // ThreadID maps to SessionID
	assert.Equal(t, "user-7", run.UserID)
	assert.Equal(t, "agent-abc", run.AgentID)
	assert.Equal(t, "completed", run.Status)
}

func TestStartChatRun_UsesLastUserMessageAsPrompt(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.ChatRunRequest{
		ThreadID: "t1",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "first message"},
			{Role: model.RoleAssistant, Content: "assistant reply"},
			{Role: model.RoleUser, Content: "follow-up question"},
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartChatRun(context.Background(), req, rr))

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "follow-up question", run.Prompt)
}

// ---------------------------------------------------------------------------
// StartAutomationRun – stream mode
// ---------------------------------------------------------------------------

func TestStartAutomationRun_StreamMode_EmitsEvents(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:       "scheduler",
		JobType:      "report",
		WorkflowID:   "wf-1",
		Prompt:       "generate the weekly report",
		ResponseMode: "stream",
	}

	rr := httptest.NewRecorder()
	err := svc.StartAutomationRun(context.Background(), req, rr)
	require.NoError(t, err)

	body := rr.Body.String()
	assert.Contains(t, body, "run.created")
	assert.Contains(t, body, "run.in_progress")
	assert.Contains(t, body, "run.completed")
}

func TestStartAutomationRun_StreamMode_EmitsModelSelected(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:       "kulrs",
		JobType:      "summarise",
		Prompt:       "summarise this",
		ResponseMode: "stream",
		ModelPreferences: &service.ModelPreferences{
			Preferred: "llama3",
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartAutomationRun(context.Background(), req, rr))

	body := rr.Body.String()
	assert.Contains(t, body, "run.model_selected")
	assert.Contains(t, body, "llama3")
}

func TestStartAutomationRun_StreamMode_PersistsContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:       "worker",
		JobType:      "ingest",
		WorkflowID:   "wf-abc",
		Prompt:       "ingest the data",
		ResponseMode: "stream",
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartAutomationRun(context.Background(), req, rr))

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "automation", run.Source)
	assert.Equal(t, "ingest", run.JobType)
	assert.Equal(t, "wf-abc", run.WorkflowID)
	assert.Equal(t, "completed", run.Status)
}

// ---------------------------------------------------------------------------
// StartAutomationRun – sync mode
// ---------------------------------------------------------------------------

func TestStartAutomationRun_SyncMode_ReturnsJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:       "scheduler",
		JobType:      "report",
		Prompt:       "run the report",
		ResponseMode: "sync",
	}

	rr := httptest.NewRecorder()
	err := svc.StartAutomationRun(context.Background(), req, rr)
	require.NoError(t, err)

	// Sync mode must not emit SSE events.
	body := rr.Body.String()
	assert.False(t, strings.Contains(body, "run.created"), "sync mode should not emit SSE events")

	// Response must be a valid AutomationRunResult JSON.
	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(strings.NewReader(body)).Decode(&result))
	assert.Equal(t, "completed", result.Status)
	assert.NotEmpty(t, result.RunID)
}

func TestStartAutomationRun_DefaultSync_ReturnsJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	// Omitting ResponseMode defaults to sync.
	req := &service.AutomationRunRequest{
		Source:  "worker",
		JobType: "process",
		Prompt:  "do the work",
	}

	rr := httptest.NewRecorder()
	err := svc.StartAutomationRun(context.Background(), req, rr)
	require.NoError(t, err)

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
	assert.NotEmpty(t, result.RunID)
}

func TestStartAutomationRun_SyncMode_ModelBackendInResult(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:       "scheduler",
		JobType:      "report",
		Prompt:       "run report",
		ResponseMode: "sync",
		ModelPreferences: &service.ModelPreferences{
			Preferred: "llama3",
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartAutomationRun(context.Background(), req, rr))

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "llama3", result.ModelBackend)
}

func TestStartAutomationRun_SyncMode_PersistsAutomationIdentityAndReturnsUsage(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &usageProvider{}, 10)

	req := &service.AutomationRunRequest{
		RequestID:    "req-auto-1",
		Source:       "kulrs",
		JobType:      "palette_analysis",
		WorkflowID:   "wf-auto-1",
		ThreadID:     "thread-auto-1",
		UserID:       "user-auto-1",
		AgentID:      "agent-auto-1",
		SystemPrompt: "Be concise.",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Summarize the product palette."},
		},
		ResponseMode: "sync",
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartAutomationRun(context.Background(), req, rr))

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
	require.NotNil(t, result.Usage)
	assert.Equal(t, 20, result.Usage.TotalTokens)

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "req-auto-1", run.RequestID)
	assert.Equal(t, "thread-auto-1", run.ThreadID)
	assert.Equal(t, "thread-auto-1", run.SessionID)
	assert.Equal(t, "user-auto-1", run.UserID)
	assert.Equal(t, "agent-auto-1", run.AgentID)
	assert.Equal(t, "wf-auto-1", run.WorkflowID)
	assert.Equal(t, "palette_analysis", run.JobType)
}

func TestStartAutomationRun_SyncMode_ReturnsStructuredOutput(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &jsonProvider{}, 10)

	req := &service.AutomationRunRequest{
		Source:  "kulrs",
		JobType: "palette_generation",
		Prompt:  "Generate a palette.",
		ResultFormat: &service.ResultFormatSpec{
			Type:         "json_object",
			RequiredKeys: []string{"name", "description", "colors"},
		},
		ResponseMode: "sync",
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartAutomationRun(context.Background(), req, rr))

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	structured, ok := result.StructuredOutput.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Voltage Mirage", structured["name"])
	assert.NotNil(t, structured["colors"])

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	storedStructured, ok := run.StructuredOutput.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Voltage Mirage", storedStructured["name"])
}

// ---------------------------------------------------------------------------
// StartKulrsPaletteRun
// ---------------------------------------------------------------------------

func TestStartKulrsPaletteRun_ReturnsCompletedResult(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &kulrsAnalysisProvider{}, 10)

	req := &service.KulrsPaletteRequest{
		ProductID: "prod-1",
		ImageURLs: []string{"https://cdn.example.com/a.jpg"},
	}

	rr := httptest.NewRecorder()
	err := svc.StartKulrsPaletteRun(context.Background(), req, rr)
	require.NoError(t, err)

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
	assert.NotEmpty(t, result.RunID)
	require.NotNil(t, result.StructuredOutput)
}

func TestStartKulrsPaletteRun_PersistsCorrectRunContext(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &kulrsAnalysisProvider{}, 10)

	req := &service.KulrsPaletteRequest{
		ProductID:  "prod-42",
		ImageURLs:  []string{"https://cdn.example.com/img.jpg"},
		WorkflowID: "wf-pal-1",
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartKulrsPaletteRun(context.Background(), req, rr))

	var run *store.Run
	for _, r := range ms.runs {
		run = r
		break
	}
	require.NotNil(t, run)
	assert.Equal(t, "automation", run.Source)
	assert.Equal(t, "palette_analysis", run.JobType)
	assert.Equal(t, "wf-pal-1", run.WorkflowID)
	assert.Contains(t, run.Prompt, "prod-42")
	assert.Contains(t, run.Prompt, "https://cdn.example.com/img.jpg")
}

func TestStartKulrsPaletteRun_WithModelPreferences(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &kulrsAnalysisProvider{}, 10)

	req := &service.KulrsPaletteRequest{
		ProductID: "prod-7",
		ImageURLs: []string{"https://cdn.example.com/x.jpg"},
		ModelPreferences: &service.ModelPreferences{
			Preferred: "llama3",
		},
	}

	rr := httptest.NewRecorder()
	require.NoError(t, svc.StartKulrsPaletteRun(context.Background(), req, rr))

	var result service.AutomationRunResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, "llama3", result.ModelBackend)
}
