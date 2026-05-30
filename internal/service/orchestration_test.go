package service_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type captureProvider struct {
	request model.Request
}

func (p *captureProvider) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	p.request = req
	return &model.Response{Content: "captured", FinishReason: "stop"}, nil
}

func (p *captureProvider) Stream(_ context.Context, req model.Request, onChunk func(string) error) error {
	p.request = req
	return onChunk("captured")
}

func writeAgentCatalogConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
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

func TestListAgentsLoadsControlPlaneCatalog(t *testing.T) {
	catalogPath := writeAgentCatalogConfig(t, `{
	"serviceProfiles": {
		"gatewayChatPlatform": {
			"agents": [
				{
					"id": "planner",
					"name": "Planner",
					"icon": "PL",
					"color": "#354F52",
					"providerName": "lm-studio-a",
					"model": "planner-model",
					"costClass": "free",
					"systemPrompt": "Keep the plan tight.",
					"endpointConfig": {
						"baseUrl": "https://private.example.test/v1",
						"apiKey": "secret",
						"modelParams": {
							"ttsVoiceId": "ariel"
						}
					},
					"enabled": true
				},
				{
					"id": "disabled",
					"name": "Disabled",
					"enabled": false
				}
			]
		}
	}
}`)
	svc := service.NewWithOptions(newMockStore(), &mockProvider{}, 10, service.ServiceOptions{AgentCatalogPath: catalogPath})

	agents := svc.ListAgents()

	require.Len(t, agents, 1)
	assert.Equal(t, "planner", agents[0].ID)
	assert.Equal(t, "remote", agents[0].Source)
	assert.Equal(t, "ariel", agents[0].TTSVoiceID)
	assert.Empty(t, agents[0].SystemPrompt)
	assert.Empty(t, agents[0].EndpointConfig.BaseURL)
	assert.Empty(t, agents[0].EndpointConfig.APIKey)
	assert.Empty(t, agents[0].EndpointConfig.ModelParams)
}

func TestStartGatewayRunAppliesControlPlanePersona(t *testing.T) {
	catalogPath := writeAgentCatalogConfig(t, `{
	"serviceProfiles": {
		"gatewayChatPlatform": {
			"agents": [
				{
					"id": "planner",
					"name": "Planner",
					"providerName": "lm-studio-a",
					"model": "planner-model",
					"costClass": "free",
					"systemPrompt": "Keep the plan tight.",
					"enabled": true
				}
			]
		}
	}
}`)
	provider := &captureProvider{}
	svc := service.NewWithOptions(newMockStore(), provider, 10, service.ServiceOptions{AgentCatalogPath: catalogPath})
	req := &service.GatewayRunRequest{
		AgentID: "planner",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What should I do next?"},
		},
	}

	require.NoError(t, svc.StartGatewayRun(context.Background(), req, httptest.NewRecorder()))

	assert.Equal(t, "planner-model", provider.request.Model)
	require.NotEmpty(t, provider.request.Messages)
	assert.Equal(t, model.RoleSystem, provider.request.Messages[0].Role)
	assert.Contains(t, provider.request.Messages[0].Content, "You are Planner.")
	assert.Contains(t, provider.request.Messages[0].Content, "Keep the plan tight.")
}

func TestStartGatewayRunCanDisablePersonalContextForAgent(t *testing.T) {
	catalogPath := writeAgentCatalogConfig(t, `{
	"serviceProfiles": {
		"gatewayChatPlatform": {
			"agents": [
				{
					"id": "mermaid",
					"name": "Mermaid",
					"providerName": "lm-studio-a",
					"model": "story-model",
					"costClass": "free",
					"systemPrompt": "Tell playful ocean stories.",
					"personalContext": { "enabled": false },
					"enabled": true
				}
			]
		}
	}
}`)
	mockUsers.mu.Lock()
	mockUsers.memories["parent"] = map[string]store.UserMemory{
		"profile.identity.name": {Key: "profile.identity.name", Value: "Parent", Confidence: 1},
		"favorite_goal":         {Key: "favorite_goal", Value: "Build the business", Confidence: 1},
	}
	mockUsers.plans["parent"] = map[string]store.UserPlan{
		"plan-1": {ID: "plan-1", UserID: "parent", Title: "Exit corp gig", Status: "active"},
	}
	mockUsers.mu.Unlock()

	provider := &captureProvider{}
	svc := service.NewWithOptions(newMockStore(), provider, 10, service.ServiceOptions{AgentCatalogPath: catalogPath})
	req := &service.GatewayRunRequest{
		AgentID: "mermaid",
		UserID:  "parent",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Tell my daughter a story."},
		},
	}

	require.NoError(t, svc.StartGatewayRun(context.Background(), req, httptest.NewRecorder()))
	require.Len(t, provider.request.Messages, 3)
	assert.Contains(t, provider.request.Messages[0].Content, "You are Mermaid.")
	assert.Contains(t, provider.request.Messages[1].Content, "configured without personal context")
	assert.NotContains(t, provider.request.Messages[1].Content, "User profile:")
	assert.NotContains(t, provider.request.Messages[1].Content, "Active plans:")
	assert.NotContains(t, provider.request.Messages[1].Content, "Exit corp gig")
	assert.NotContains(t, provider.request.Messages[1].Content, "Build the business")
}

func TestStartGatewayRunUsesCatalogEndpointOverride(t *testing.T) {
	catalogPath := writeAgentCatalogConfig(t, `{
	"serviceProfiles": {
		"gatewayChatPlatform": {
			"agents": [
				{
					"id": "expert-planner",
					"name": "Expert Planner",
					"providerName": "openai-main",
					"model": "planning-model",
					"costClass": "premium",
					"systemPrompt": "Think carefully.",
					"endpointConfig": {
						"baseUrl": "https://api.example.test/v1",
						"apiKey": "test-api-key"
					},
					"enabled": true
				}
			]
		}
	}
}`)
	provider := &captureProvider{}
	svc := service.NewWithOptions(newMockStore(), provider, 10, service.ServiceOptions{
		AgentCatalogPath: catalogPath,
		ChatNode:         "papai",
	})
	req := &service.GatewayRunRequest{
		AgentID: "expert-planner",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Build a plan."},
		},
	}

	require.NoError(t, svc.StartGatewayRun(context.Background(), req, httptest.NewRecorder()))

	assert.Equal(t, "planning-model", provider.request.Model)
	assert.Empty(t, provider.request.BackendNode)
	assert.Equal(t, "https://api.example.test/v1", provider.request.BackendBaseURL)
	assert.Equal(t, "test-api-key", provider.request.BackendAPIKey)
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

func TestEffectiveAutomationToolPolicy_DeniesCreateScheduleForReminderJobs(t *testing.T) {
	spec := service.EffectiveAutomationToolPolicyForTest(&service.AutomationRunRequest{
		JobType: "reminder",
		ToolPolicy: &service.ToolPolicySpec{
			AllowedTools:    []string{"create_schedule", "memory_write"},
			RequireApproval: []string{"file"},
		},
	})

	require.NotNil(t, spec)
	assert.Contains(t, spec.DeniedTools, "create_schedule")
	assert.Contains(t, spec.DeniedTools, "create_project_checkin")
	assert.Contains(t, spec.AllowedTools, "create_schedule")
	assert.Contains(t, spec.RequireApproval, "file")
}

func TestEffectiveAutomationToolPolicy_DeniesSchedulingForProjectCheckins(t *testing.T) {
	spec := service.EffectiveAutomationToolPolicyForTest(&service.AutomationRunRequest{
		JobType: "project_checkin",
		ToolPolicy: &service.ToolPolicySpec{
			AllowedTools: []string{"create_schedule", "create_project_checkin", "plan_upsert"},
		},
	})

	require.NotNil(t, spec)
	assert.Contains(t, spec.DeniedTools, "create_schedule")
	assert.Contains(t, spec.DeniedTools, "create_project_checkin")
	assert.Contains(t, spec.AllowedTools, "plan_upsert")
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
