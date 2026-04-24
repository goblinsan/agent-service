package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

// ToolPolicySpec configures tool access rules for a run.
type ToolPolicySpec struct {
	// AllowedTools limits which tool names the agent may call.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// RequireApproval lists tools that must receive human approval before execution.
	RequireApproval []string `json:"require_approval,omitempty"`
	// DeniedTools lists tools that are completely blocked for this run.
	DeniedTools []string `json:"denied_tools,omitempty"`
}

// ModelPreferences expresses model-selection hints for a run.
type ModelPreferences struct {
	// Preferred is the name of the first-choice model backend.
	Preferred string `json:"preferred,omitempty"`
	// Fallbacks is an ordered list of alternative backends to try if Preferred
	// is unavailable.
	Fallbacks []string `json:"fallbacks,omitempty"`
	// MaxTokens caps the response length for this run.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// ChatRunRequest is the internal orchestration contract accepted from the
// gateway-chat-platform.  It carries the full normalised context so that
// agent-service never has to contact the gateway again during execution.
type ChatRunRequest struct {
	// RequestID is the correlation ID assigned by the gateway.
	RequestID string `json:"request_id"`
	// ThreadID identifies the chat conversation (maps to a session).
	ThreadID string `json:"thread_id"`
	// UserID is the authenticated end-user.
	UserID string `json:"user_id"`
	// AgentID selects which agent definition to run.
	AgentID string `json:"agent_id"`
	// Messages is the full conversation history to pass to the model.
	Messages []model.Message `json:"messages"`
	// SystemPrompt is an optional override for the agent's system instruction.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// ToolPolicy overrides the default tool access rules for this request.
	ToolPolicy *ToolPolicySpec `json:"tool_policy,omitempty"`
	// ModelPreferences expresses the caller's model-selection preferences.
	ModelPreferences *ModelPreferences `json:"model_preferences,omitempty"`
	// Metadata is a free-form bag of key/value pairs forwarded from the gateway.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AutomationRunRequest is the internal orchestration contract accepted from
// non-chat callers such as scheduled jobs, background workers, and workflow
// engines.
type AutomationRunRequest struct {
	// Source identifies the automation system (e.g. "scheduler", "kulrs").
	Source string `json:"source"`
	// JobType classifies the kind of work (e.g. "report", "summarise").
	JobType string `json:"job_type"`
	// WorkflowID links the run to a parent workflow, if applicable.
	WorkflowID string `json:"workflow_id,omitempty"`
	// Prompt is the primary instruction for the agent.
	Prompt string `json:"prompt,omitempty"`
	// Context is a free-form map of additional data passed as context.
	Context map[string]any `json:"context,omitempty"`
	// ModelPreferences expresses the caller's model-selection preferences.
	ModelPreferences *ModelPreferences `json:"model_preferences,omitempty"`
	// ResponseMode controls whether the response is streamed ("stream") or
	// returned as a single JSON object ("sync").  Defaults to "stream".
	ResponseMode string `json:"response_mode,omitempty"`
	// Metadata is a free-form bag of additional key/value pairs.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AutomationRunResult is the response body for sync-mode automation runs.
type AutomationRunResult struct {
	RunID        string              `json:"run_id"`
	Status       string              `json:"status"`
	Output       string              `json:"output,omitempty"`
	ModelBackend string              `json:"model_backend,omitempty"`
	ToolCalls    []store.RunToolCall `json:"tool_calls,omitempty"`
}

// StartChatRun creates a run from a gateway-chat-platform request and streams
// structured SSE events to w until the run completes or fails.
func (s *Service) StartChatRun(ctx context.Context, req *ChatRunRequest, w http.ResponseWriter) error {
	// Derive a prompt string for the run record: use the last user message from
	// the conversation history if present; otherwise fall back to SystemPrompt.
	// The full message history is passed to the model separately by the agent.
	prompt := req.SystemPrompt
	for _, m := range req.Messages {
		if m.Role == model.RoleUser {
			prompt = m.Content
		}
	}

	modelBackend := ""
	if req.ModelPreferences != nil {
		modelBackend = req.ModelPreferences.Preferred
	}

	run := &store.Run{
		ID:           newID(),
		SessionID:    req.ThreadID,
		Source:       string(store.RunSourceChat),
		Prompt:       prompt,
		Status:       "created",
		ModelBackend: modelBackend,
		RequestID:    req.RequestID,
		ThreadID:     req.ThreadID,
		UserID:       req.UserID,
		AgentID:      req.AgentID,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if err := sse.Write(w, sse.Event{Type: sse.EventRunCreated, Data: run}); err != nil {
		return err
	}

	if modelBackend != "" {
		if err := sse.Write(w, sse.Event{
			Type: sse.EventRunModelSelected,
			Data: sse.ModelSelectedPayload{RunID: run.ID, Backend: modelBackend},
		}); err != nil {
			return err
		}
	}

	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	if err := sse.Write(w, sse.Event{Type: sse.EventRunInProgress, Data: run}); err != nil {
		return err
	}

	if err := s.agent.Run(ctx, run, w, buildRunPolicy(req.ToolPolicy)); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: run})
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	return sse.Write(w, sse.Event{Type: sse.EventRunCompleted, Data: run})
}

// StartAutomationRun creates a run from an automation caller's request.
// When ResponseMode is "stream", structured SSE events are written to w.
// When ResponseMode is "sync" or empty (the default), the agent runs to
// completion and the result is written as a JSON AutomationRunResult object.
func (s *Service) StartAutomationRun(ctx context.Context, req *AutomationRunRequest, w http.ResponseWriter) error {
	modelBackend := ""
	if req.ModelPreferences != nil {
		modelBackend = req.ModelPreferences.Preferred
	}

	run := &store.Run{
		ID:           newID(),
		Source:       string(store.RunSourceAutomation),
		Prompt:       req.Prompt,
		Status:       "created",
		ModelBackend: modelBackend,
		WorkflowID:   req.WorkflowID,
		JobType:      req.JobType,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if req.ResponseMode == "stream" {
		return s.streamAutomationRun(ctx, run, w)
	}
	return s.syncAutomationRun(ctx, run, w)
}

// streamAutomationRun runs an automation request and emits SSE events to w.
func (s *Service) streamAutomationRun(ctx context.Context, run *store.Run, w http.ResponseWriter) error {
	if err := sse.Write(w, sse.Event{Type: sse.EventRunCreated, Data: run}); err != nil {
		return err
	}

	if run.ModelBackend != "" {
		if err := sse.Write(w, sse.Event{
			Type: sse.EventRunModelSelected,
			Data: sse.ModelSelectedPayload{RunID: run.ID, Backend: run.ModelBackend},
		}); err != nil {
			return err
		}
	}

	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	if err := sse.Write(w, sse.Event{Type: sse.EventRunInProgress, Data: run}); err != nil {
		return err
	}

	if err := s.agent.Run(ctx, run, w, nil); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: run})
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	return sse.Write(w, sse.Event{Type: sse.EventRunCompleted, Data: run})
}

// syncAutomationRun runs an automation request to completion and writes the
// result as a single JSON response.
func (s *Service) syncAutomationRun(ctx context.Context, run *store.Run, w http.ResponseWriter) error {
	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}

	dw := &discardResponseWriter{}
	if err := s.agent.Run(ctx, run, dw, nil); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}

	return json.NewEncoder(w).Encode(AutomationRunResult{
		RunID:        run.ID,
		Status:       "completed",
		Output:       run.Response,
		ModelBackend: run.ModelBackend,
		ToolCalls:    run.ToolCalls,
	})
}

// discardResponseWriter is an http.ResponseWriter that silently discards all
// writes.  It is used in sync-mode automation runs to suppress SSE events from
// the agent loop.
type discardResponseWriter struct {
	header http.Header
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardResponseWriter) WriteHeader(_ int)           {}

// ── Kulrs automation ──────────────────────────────────────────────────────────

// KulrsPaletteRequest is the domain payload accepted from the Kulrs automation
// system for color-palette analysis jobs.  It is a first-class automation
// caller type that does not use gateway-chat semantics.
type KulrsPaletteRequest struct {
	// WorkflowID links the run to a parent Kulrs workflow, if applicable.
	WorkflowID string `json:"workflow_id,omitempty"`
	// ProductID identifies the product whose palette should be analysed.
	ProductID string `json:"product_id"`
	// ImageURLs is the list of product image URLs from which to derive the palette.
	ImageURLs []string `json:"image_urls"`
	// ModelPreferences expresses the caller's model-selection preferences.
	ModelPreferences *ModelPreferences `json:"model_preferences,omitempty"`
	// Metadata is a free-form bag for caller-specific key/value pairs.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// StartKulrsPaletteRun creates and executes a Kulrs color-palette analysis run.
// It always operates in sync mode and writes a single JSON AutomationRunResult
// to w so that the Kulrs caller can consume structured results directly.
func (s *Service) StartKulrsPaletteRun(ctx context.Context, req *KulrsPaletteRequest, w http.ResponseWriter) error {
	auto := &AutomationRunRequest{
		Source:           "kulrs",
		JobType:          "palette_analysis",
		WorkflowID:       req.WorkflowID,
		Prompt:           buildPalettePrompt(req.ProductID, req.ImageURLs),
		ModelPreferences: req.ModelPreferences,
		Metadata:         req.Metadata,
		ResponseMode:     "sync",
	}
	return s.StartAutomationRun(ctx, auto, w)
}

// buildPalettePrompt constructs the agent instruction for a palette analysis job.
func buildPalettePrompt(productID string, imageURLs []string) string {
	return fmt.Sprintf(
		"Analyze the color palette for product %s from the following images: %s. "+
			"Return the dominant colors, accent colors, and a short palette description.",
		productID,
		strings.Join(imageURLs, ", "),
	)
}
