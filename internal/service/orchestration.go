package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
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
	// RequestID is the caller-assigned correlation ID for this automation run.
	RequestID string `json:"request_id,omitempty"`
	// Source identifies the automation system (e.g. "scheduler", "kulrs").
	Source string `json:"source"`
	// JobType classifies the kind of work (e.g. "report", "summarise").
	JobType string `json:"job_type"`
	// WorkflowID links the run to a parent workflow, if applicable.
	WorkflowID string `json:"workflow_id,omitempty"`
	// ThreadID optionally associates the automation run with a durable thread or
	// inbox conversation owned by the caller.
	ThreadID string `json:"thread_id,omitempty"`
	// UserID identifies the end user or automation owner, when applicable.
	UserID string `json:"user_id,omitempty"`
	// AgentID identifies the logical assistant handling this automation run.
	AgentID string `json:"agent_id,omitempty"`
	// Prompt is the primary instruction for the agent.
	Prompt string `json:"prompt,omitempty"`
	// Messages, when provided, carry the full normalized conversation/history for
	// the automation run and take precedence over Prompt+Context prompt building.
	Messages []model.Message `json:"messages,omitempty"`
	// SystemPrompt is an optional automation-specific system instruction.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Context is a free-form map of additional data passed as context.
	Context map[string]any `json:"context,omitempty"`
	// ToolPolicy overrides the default tool access rules for this request.
	ToolPolicy *ToolPolicySpec `json:"tool_policy,omitempty"`
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
	RunID              string                    `json:"run_id"`
	Status             string                    `json:"status"`
	Output             string                    `json:"output,omitempty"`
	ModelBackend       string                    `json:"model_backend,omitempty"`
	ToolCalls          []store.RunToolCall       `json:"tool_calls,omitempty"`
	ApprovalRecords    []store.RunApprovalRecord `json:"approval_records,omitempty"`
	Usage              *model.Usage              `json:"usage,omitempty"`
	OrchestrationState *OrchestrationState       `json:"orchestration_state,omitempty"`
}

type OrchestrationState struct {
	RunID             string         `json:"runId,omitempty"`
	CheckpointID      string         `json:"checkpointId,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	RequiredApprovers []string       `json:"requiredApprovers,omitempty"`
	ToolName          string         `json:"toolName,omitempty"`
	ToolParams        map[string]any `json:"toolParams,omitempty"`
}

// GatewayRunRequest is the sync JSON contract currently used by the
// gateway-chat-platform internal agent-service client.
type GatewayRunRequest struct {
	AgentID        string          `json:"agentId"`
	Model          string          `json:"model,omitempty"`
	Messages       []model.Message `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"maxTokens,omitempty"`
	ModelParams    map[string]any  `json:"modelParams,omitempty"`
	WorkflowID     string          `json:"workflowId,omitempty"`
	WorkflowSource string          `json:"workflowSource,omitempty"`
	DeliveryMode   string          `json:"deliveryMode,omitempty"`
	UserID         string          `json:"userId,omitempty"`
	ChannelID      string          `json:"channelId,omitempty"`
	ThreadID       string          `json:"threadId,omitempty"`
}

// GatewayRunResponse mirrors the gateway's existing internal client contract.
type GatewayRunResponse struct {
	AgentID      string `json:"agentId"`
	UsedProvider string `json:"usedProvider"`
	Model        string `json:"model"`
	Message      struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Status             string `json:"status,omitempty"`
	OrchestrationState *struct {
		RunID             string   `json:"runId,omitempty"`
		CheckpointID      string   `json:"checkpointId,omitempty"`
		Reason            string   `json:"reason,omitempty"`
		RequiredApprovers []string `json:"requiredApprovers,omitempty"`
	} `json:"orchestrationState,omitempty"`
	ResultThreadID string `json:"resultThreadId,omitempty"`
}

// StartChatRun creates a run from a gateway-chat-platform request and streams
// structured SSE events to w until the run completes or fails.
func (s *Service) StartChatRun(ctx context.Context, req *ChatRunRequest, w http.ResponseWriter) error {
	// Derive a prompt string for the run record while preserving the full message
	// history for the model call itself.
	initialMessages := buildChatMessages(req)
	prompt := derivePrompt(initialMessages, req.SystemPrompt)

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

	slog.Info("chat run created",
		"run_id", run.ID,
		"request_id", run.RequestID,
		"thread_id", run.ThreadID,
		"user_id", run.UserID,
		"agent_id", run.AgentID,
		"model_backend", run.ModelBackend,
	)

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

	observer := newObservedRunWriter(w)
	done := s.startManagedRun(run, observer, buildRunPolicy(req.ToolPolicy), initialMessages)
	paused, err := s.waitForRunOutcome(ctx, run, observer, done)
	if paused != nil {
		return nil
	}
	return err
}

// StartGatewayRun executes the gateway-chat-platform's current sync JSON
// contract while preserving the full conversation context passed by the caller.
func (s *Service) StartGatewayRun(ctx context.Context, req *GatewayRunRequest, w http.ResponseWriter) error {
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	source := string(store.RunSourceChat)
	if req.WorkflowID != "" || req.DeliveryMode != "" || req.ChannelID != "" {
		source = string(store.RunSourceAutomation)
	}
	run := &store.Run{
		ID:           newID(),
		SessionID:    req.ThreadID,
		Source:       source,
		Prompt:       derivePrompt(req.Messages, ""),
		Status:       "created",
		ModelBackend: req.Model,
		ThreadID:     req.ThreadID,
		UserID:       req.UserID,
		AgentID:      req.AgentID,
		WorkflowID:   req.WorkflowID,
		JobType:      req.WorkflowSource,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	dw := &discardResponseWriter{}
	start := time.Now()
	if err := s.agent.RunWithMessages(ctx, run, dw, nil, req.Messages); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		s.recordRunMetrics(run, time.Since(start))
		return fmt.Errorf("agent run: %w", err)
	}
	run.Status = "completed"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	s.recordRunMetrics(run, time.Since(start))
	resp := GatewayRunResponse{
		AgentID:        req.AgentID,
		UsedProvider:   "agent-service",
		Model:          firstNonEmpty(run.ModelBackend, req.Model),
		ResultThreadID: req.ThreadID,
	}
	resp.Message.Role = string(model.RoleAssistant)
	resp.Message.Content = run.Response
	return json.NewEncoder(w).Encode(resp)
}

// StartChatRunSync creates a run from a gateway-chat-platform request and
// returns a single JSON response instead of streaming SSE events.
func (s *Service) StartChatRunSync(ctx context.Context, req *ChatRunRequest, w http.ResponseWriter) error {
	initialMessages := buildChatMessages(req)
	prompt := derivePrompt(initialMessages, req.SystemPrompt)

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
	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	observer := newObservedRunWriter(nil)
	done := s.startManagedRun(run, observer, buildRunPolicy(req.ToolPolicy), initialMessages)
	paused, err := s.waitForRunOutcome(ctx, run, observer, done)
	if err != nil {
		return err
	}
	resp := GatewayRunResponse{
		AgentID:        req.AgentID,
		UsedProvider:   "agent-service",
		Model:          firstNonEmpty(run.ModelBackend, preferredModel(req.ModelPreferences)),
		ResultThreadID: req.ThreadID,
	}
	if paused != nil {
		resp.Status = "approval_required"
		resp.OrchestrationState = &struct {
			RunID             string   `json:"runId,omitempty"`
			CheckpointID      string   `json:"checkpointId,omitempty"`
			Reason            string   `json:"reason,omitempty"`
			RequiredApprovers []string `json:"requiredApprovers,omitempty"`
		}{
			RunID:             paused.RunID,
			CheckpointID:      paused.CheckpointID,
			Reason:            paused.Reason,
			RequiredApprovers: paused.RequiredApprovers,
		}
		return json.NewEncoder(w).Encode(resp)
	}
	resp.Message.Role = string(model.RoleAssistant)
	resp.Message.Content = run.Response
	return json.NewEncoder(w).Encode(resp)
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

	initialMessages := buildAutomationMessages(req)
	run := &store.Run{
		ID:           newID(),
		SessionID:    req.ThreadID,
		Source:       string(store.RunSourceAutomation),
		Prompt:       derivePrompt(initialMessages, req.Prompt),
		Status:       "created",
		ModelBackend: modelBackend,
		RequestID:    req.RequestID,
		ThreadID:     req.ThreadID,
		UserID:       req.UserID,
		AgentID:      req.AgentID,
		WorkflowID:   req.WorkflowID,
		JobType:      req.JobType,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if req.ResponseMode == "stream" {
		return s.streamAutomationRun(ctx, run, w, initialMessages, buildRunPolicy(req.ToolPolicy))
	}
	return s.syncAutomationRun(ctx, run, w, initialMessages, buildRunPolicy(req.ToolPolicy))
}

// streamAutomationRun runs an automation request and emits SSE events to w.
func (s *Service) streamAutomationRun(ctx context.Context, run *store.Run, w http.ResponseWriter, initialMessages []model.Message, runPolicy policy.Policy) error {
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

	slog.Info("automation run started",
		"run_id", run.ID,
		"request_id", run.RequestID,
		"thread_id", run.ThreadID,
		"user_id", run.UserID,
		"agent_id", run.AgentID,
		"workflow_id", run.WorkflowID,
		"job_type", run.JobType,
		"model_backend", run.ModelBackend,
	)
	observer := newObservedRunWriter(w)
	done := s.startManagedRun(run, observer, runPolicy, initialMessages)
	paused, err := s.waitForRunOutcome(ctx, run, observer, done)
	if paused != nil {
		return nil
	}
	return err
}

// syncAutomationRun runs an automation request to completion and writes the
// result as a single JSON response.
func (s *Service) syncAutomationRun(ctx context.Context, run *store.Run, w http.ResponseWriter, initialMessages []model.Message, runPolicy policy.Policy) error {
	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}

	slog.Info("automation run started",
		"run_id", run.ID,
		"request_id", run.RequestID,
		"thread_id", run.ThreadID,
		"user_id", run.UserID,
		"agent_id", run.AgentID,
		"workflow_id", run.WorkflowID,
		"job_type", run.JobType,
		"model_backend", run.ModelBackend,
	)
	observer := newObservedRunWriter(nil)
	done := s.startManagedRun(run, observer, runPolicy, initialMessages)
	paused, err := s.waitForRunOutcome(ctx, run, observer, done)
	if err != nil {
		return err
	}

	resp := AutomationRunResult{
		RunID:           run.ID,
		Status:          "completed",
		Output:          run.Response,
		ModelBackend:    run.ModelBackend,
		ToolCalls:       run.ToolCalls,
		ApprovalRecords: run.ApprovalRecs,
	}
	if run.Usage.TotalTokens > 0 || run.Usage.PromptTokens > 0 || run.Usage.CompletionTokens > 0 {
		usageCopy := run.Usage
		resp.Usage = &usageCopy
	}
	if paused != nil {
		resp.Status = "approval_required"
		resp.OrchestrationState = paused
		resp.Output = ""
	}
	return json.NewEncoder(w).Encode(resp)
}

type runSignalKind string

const (
	runSignalPaused    runSignalKind = "paused"
	runSignalCompleted runSignalKind = "completed"
	runSignalFailed    runSignalKind = "failed"
)

type runSignal struct {
	kind       runSignalKind
	approvalID string
	reason     string
	toolName   string
	toolParams map[string]any
}

type observedRunWriter struct {
	header         http.Header
	forward        http.ResponseWriter
	forwardEnabled bool
	mu             sync.Mutex
	signals        chan runSignal
	latestApproval *sse.ApprovalRequestedPayload
}

func newObservedRunWriter(forward http.ResponseWriter) *observedRunWriter {
	return &observedRunWriter{
		header:         make(http.Header),
		forward:        forward,
		forwardEnabled: forward != nil,
		signals:        make(chan runSignal, 16),
	}
}

func (w *observedRunWriter) Header() http.Header {
	if w.forward != nil {
		return w.forward.Header()
	}
	return w.header
}

func (w *observedRunWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.forwardEnabled || w.forward == nil {
		return len(b), nil
	}
	return w.forward.Write(b)
}

func (w *observedRunWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.forwardEnabled || w.forward == nil {
		return
	}
	w.forward.WriteHeader(statusCode)
}

func (w *observedRunWriter) ObserveEvent(event sse.Event) {
	switch event.Type {
	case sse.EventRunApprovalRequested:
		if payload, ok := event.Data.(sse.ApprovalRequestedPayload); ok {
			payloadCopy := payload
			w.latestApproval = &payloadCopy
		}
	case sse.EventRunPaused:
		signal := runSignal{kind: runSignalPaused}
		if w.latestApproval != nil {
			signal.approvalID = w.latestApproval.ApprovalID
			signal.reason = w.latestApproval.Reason
			signal.toolName = w.latestApproval.ToolName
			signal.toolParams = w.latestApproval.Params
		}
		w.emitSignal(signal)
	case sse.EventRunCompleted:
		w.emitSignal(runSignal{kind: runSignalCompleted})
	case sse.EventRunFailed:
		w.emitSignal(runSignal{kind: runSignalFailed})
	}
}

func (w *observedRunWriter) DisableForwarding() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.forwardEnabled = false
}

func (w *observedRunWriter) emitSignal(signal runSignal) {
	select {
	case w.signals <- signal:
	default:
	}
}

func (s *Service) startManagedRun(run *store.Run, writer http.ResponseWriter, runPolicy policy.Policy, initialMessages []model.Message) <-chan error {
	done := make(chan error, 1)
	go func() {
		start := time.Now()
		err := s.agent.RunWithMessages(context.Background(), run, writer, runPolicy, initialMessages)
		if err != nil {
			run.Status = "failed"
			run.UpdatedAt = time.Now().UTC()
			_ = s.store.UpdateRun(context.Background(), run)
			_ = sse.Write(writer, sse.Event{Type: sse.EventRunFailed, Data: run})
			s.recordRunMetrics(run, time.Since(start))
			done <- fmt.Errorf("agent run: %w", err)
			return
		}

		run.Status = "completed"
		run.PausedState = ""
		run.UpdatedAt = time.Now().UTC()
		if err := s.store.UpdateRun(context.Background(), run); err != nil {
			done <- fmt.Errorf("update run completed: %w", err)
			return
		}
		s.recordRunMetrics(run, time.Since(start))
		done <- sse.Write(writer, sse.Event{Type: sse.EventRunCompleted, Data: run})
	}()
	return done
}

func (s *Service) waitForRunOutcome(ctx context.Context, run *store.Run, writer *observedRunWriter, done <-chan error) (*OrchestrationState, error) {
	for {
		select {
		case signal := <-writer.signals:
			if signal.kind != runSignalPaused {
				continue
			}
			run.PausedState = buildPausedState(signal.approvalID, signal.reason, signal.toolName, signal.toolParams)
			run.UpdatedAt = time.Now().UTC()
			_ = s.store.UpdateRun(context.Background(), run)
			writer.DisableForwarding()
			return &OrchestrationState{
				RunID:        run.ID,
				CheckpointID: signal.approvalID,
				Reason:       firstNonEmpty(signal.reason, "Awaiting approval"),
				ToolName:     signal.toolName,
				ToolParams:   signal.toolParams,
			}, nil
		case err := <-done:
			return nil, err
		case <-ctx.Done():
			writer.DisableForwarding()
			return nil, ctx.Err()
		}
	}
}

func buildPausedState(approvalID, reason, toolName string, toolParams map[string]any) string {
	state := map[string]any{
		"type":        "approval_required",
		"approval_id": approvalID,
		"reason":      reason,
		"tool_name":   toolName,
		"tool_params": toolParams,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(raw)
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

func buildChatMessages(req *ChatRunRequest) []model.Message {
	messages := make([]model.Message, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, model.Message{Role: model.RoleSystem, Content: req.SystemPrompt})
	}
	messages = append(messages, req.Messages...)
	return messages
}

func buildAutomationMessages(req *AutomationRunRequest) []model.Message {
	var messages []model.Message
	if req.SystemPrompt != "" {
		messages = append(messages, model.Message{Role: model.RoleSystem, Content: req.SystemPrompt})
	}
	var automationContext []string
	if req.Source != "" {
		automationContext = append(automationContext, fmt.Sprintf("Automation source: %s", req.Source))
	}
	if req.JobType != "" {
		automationContext = append(automationContext, fmt.Sprintf("Job type: %s", req.JobType))
	}
	if req.WorkflowID != "" {
		automationContext = append(automationContext, fmt.Sprintf("Workflow ID: %s", req.WorkflowID))
	}
	if req.RequestID != "" {
		automationContext = append(automationContext, fmt.Sprintf("Request ID: %s", req.RequestID))
	}
	if req.AgentID != "" {
		automationContext = append(automationContext, fmt.Sprintf("Agent ID: %s", req.AgentID))
	}
	if req.ThreadID != "" {
		automationContext = append(automationContext, fmt.Sprintf("Thread ID: %s", req.ThreadID))
	}
	if req.UserID != "" {
		automationContext = append(automationContext, fmt.Sprintf("User ID: %s", req.UserID))
	}
	if len(automationContext) > 0 {
		messages = append(messages, model.Message{Role: model.RoleSystem, Content: strings.Join(automationContext, "\n")})
	}
	if len(req.Messages) > 0 {
		return append(messages, req.Messages...)
	}
	userContent := req.Prompt
	if len(req.Context) > 0 {
		if raw, err := json.Marshal(req.Context); err == nil {
			userContent = fmt.Sprintf("%s\n\nContext JSON:\n%s", userContent, string(raw))
		}
	}
	if len(req.Metadata) > 0 {
		if raw, err := json.Marshal(req.Metadata); err == nil {
			userContent = fmt.Sprintf("%s\n\nMetadata JSON:\n%s", userContent, string(raw))
		}
	}
	return append(messages, model.Message{Role: model.RoleUser, Content: strings.TrimSpace(userContent)})
}

func derivePrompt(messages []model.Message, fallback string) string {
	prompt := fallback
	for _, m := range messages {
		if m.Role == model.RoleUser {
			prompt = m.Content
		}
	}
	if prompt == "" && len(messages) > 0 {
		prompt = messages[len(messages)-1].Content
	}
	return prompt
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func preferredModel(prefs *ModelPreferences) string {
	if prefs == nil {
		return ""
	}
	return prefs.Preferred
}
