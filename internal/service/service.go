package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/goblinsan/agent-service/internal/agent"
	"github.com/goblinsan/agent-service/internal/metrics"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/runner"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

// ServiceOptions configures optional capabilities of the Service.
type ServiceOptions struct {
	// Runner is used by the agent to execute tool calls. When nil, any tool call
	// requested by the model will result in an error recorded in the run.
	Runner runner.Runner
	// Policy is the base policy applied to every tool call. When nil all tool
	// calls that reach a runner are allowed.
	Policy policy.Policy
	// Metrics, when non-nil, receives run-level counters (tool calls, approvals,
	// backend selections, and latency) as each run completes.
	Metrics *metrics.Metrics
	// ToolSpecs is the static list of tools advertised to the model on every
	// completion request. Should match the tools registered with Runner.
	ToolSpecs []model.ToolSpec
	// ChatNode, when non-empty, pins every chat run to the registered
	// llm-service node with this Name (e.g. "papai").  Routing by node name
	// avoids the case-sensitive, model-string matching that previously
	// caused brittle routing.  Set via the CHAT_NODE env var.
	ChatNode string
	// AutomationNode is the default registered node for automation runs that
	// arrive without an explicit model preference.  Set via the
	// AUTOMATION_NODE env var.
	AutomationNode string
	// ChatModel and AutomationModel are deprecated string-based overrides
	// kept only for backward compatibility.  Prefer ChatNode/AutomationNode.
	ChatModel       string
	AutomationModel string
	// NotificationDispatcher, when non-nil, is called after a notification row
	// is persisted so external channels (for example APNs) can be fanned out.
	NotificationDispatcher NotificationDispatcher
	// LocalTimezone is the user's/operator's IANA timezone for date-sensitive
	// system context. Empty falls back to UTC.
	LocalTimezone string
	// AgentCatalogPath optionally points at a gateway-control-plane config JSON
	// file whose serviceProfiles.gatewayChatPlatform.agents entries become
	// chat personas.
	AgentCatalogPath string
}

// NotificationDispatcher fan-outs persisted notifications to external channels.
type NotificationDispatcher interface {
	DispatchNotification(ctx context.Context, n store.Notification) error
}

type Service struct {
	store           store.Store
	agent           *agent.Agent
	approvals       *policy.Approvals
	metrics         *metrics.Metrics
	routeMu         sync.RWMutex
	chatNode        string
	automationNode  string
	chatModel       string
	automationModel string
	notifier        NotificationDispatcher
	localTimezone   string
	agentCatalog    *agentCatalog
}

// ChatNode returns the current chat-routing node name (may be empty).
func (s *Service) ChatNode() string {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.chatNode
}

// AutomationNode returns the current automation-routing node name (may be empty).
func (s *Service) AutomationNode() string {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.automationNode
}

// ChatModel returns the deprecated chat model override (may be empty).
func (s *Service) ChatModel() string {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.chatModel
}

// AutomationModel returns the deprecated automation model default (may be empty).
func (s *Service) AutomationModel() string {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.automationModel
}

// SetChatNode updates the chat-routing node name at runtime.  Pass an empty
// string to fall back to model-based selection.  The change applies to
// subsequent runs.
func (s *Service) SetChatNode(name string) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.chatNode = name
}

// SetAutomationNode updates the automation-routing node name at runtime.
func (s *Service) SetAutomationNode(name string) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.automationNode = name
}

// New returns a Service with default options (no runner, no base policy).
func New(s store.Store, p model.Provider, maxSteps int) *Service {
	return NewWithOptions(s, p, maxSteps, ServiceOptions{})
}

// NewWithOptions returns a Service configured with the supplied options.
func NewWithOptions(s store.Store, p model.Provider, maxSteps int, opts ServiceOptions) *Service {
	approvals := policy.NewApprovals()
	return &Service{
		store: s,
		agent: agent.NewWithOptions(p, s, maxSteps, agent.Options{
			Runner:    opts.Runner,
			Policy:    opts.Policy,
			Approvals: approvals,
			ToolSpecs: opts.ToolSpecs,
		}),
		approvals:       approvals,
		metrics:         opts.Metrics,
		chatNode:        opts.ChatNode,
		automationNode:  opts.AutomationNode,
		chatModel:       opts.ChatModel,
		automationModel: opts.AutomationModel,
		notifier:        opts.NotificationDispatcher,
		localTimezone:   opts.LocalTimezone,
		agentCatalog:    newAgentCatalog(opts.AgentCatalogPath),
	}
}

func (s *Service) ListAgents() []AgentConfig {
	model := firstNonEmpty(s.ChatModel(), s.ChatNode(), "agent-service")
	return s.agentCatalog.list(model)
}

func (s *Service) resolveAgent(id string) (AgentConfig, bool) {
	if s == nil || s.agentCatalog == nil {
		return AgentConfig{}, false
	}
	return s.agentCatalog.get(id)
}

func (s *Service) CreateSession(ctx context.Context, name, description string) (*store.Session, error) {
	sess := &store.Session{
		ID:          newID(),
		Name:        name,
		Description: description,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

func (s *Service) StartRun(ctx context.Context, sessionID, prompt string, w http.ResponseWriter) error {
	run := &store.Run{
		ID:        newID(),
		SessionID: sessionID,
		Source:    string(store.RunSourceChat),
		Prompt:    prompt,
		Status:    "created",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if err := sse.Write(w, sse.Event{Type: sse.EventRunCreated, Data: run}); err != nil {
		return err
	}

	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	if err := sse.Write(w, sse.Event{Type: sse.EventRunInProgress, Data: run}); err != nil {
		return err
	}

	start := time.Now()
	if err := s.agent.Run(ctx, run, w, nil); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: run})
		latency := time.Since(start)
		s.recordRunMetrics(run, latency)
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.Response = fmt.Sprintf("Completed processing: %s", prompt)
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	latency := time.Since(start)
	s.recordRunMetrics(run, latency)
	return sse.Write(w, sse.Event{Type: sse.EventRunCompleted, Data: run})
}

// RequestApproval creates a new pending approval for the given tool call and
// returns the newly created Approval record. This is called by the agent when
// a policy decision of RequireApproval is returned, and may also be used
// directly for testing.
func (s *Service) RequestApproval(toolName string, params map[string]any) *policy.Approval {
	return s.approvals.Request(toolName, params)
}

// GetApproval returns the approval record for the given ID.
func (s *Service) GetApproval(id string) (*policy.Approval, error) {
	return s.approvals.Get(id)
}

// ApproveApproval marks the pending approval as approved.
func (s *Service) ApproveApproval(id string) error {
	return s.approvals.Approve(id)
}

// DenyApproval marks the pending approval as denied with the given reason.
func (s *Service) DenyApproval(id, reason string) error {
	return s.approvals.Deny(id, reason)
}

// GetRun returns the stored run record for the given run ID. It is intended for
// operators and inspection tooling that need to review a completed run.
func (s *Service) GetRun(ctx context.Context, id string) (*store.Run, error) {
	return s.store.GetRun(ctx, id)
}

// ListRunSteps returns the ordered list of agent steps persisted for the given
// run ID. This enables replay and post-hoc inspection of individual reasoning
// steps without relying on streaming-time logs.
func (s *Service) ListRunSteps(ctx context.Context, runID string) ([]*store.RunStep, error) {
	return s.store.ListSteps(ctx, runID)
}

// ListThreadsForUser returns a summary of chat threads owned by userID,
// newest activity first.  Used by chat clients (web UI, iOS app) to render a
// thread list / sidebar so users can switch between previous conversations.
func (s *Service) ListThreadsForUser(ctx context.Context, userID string, limit int) ([]store.ThreadSummary, error) {
	return s.store.ListThreadsForUser(ctx, userID, limit)
}

// GetThreadForUser returns the ordered message history for a thread, scoped to
// the requesting user.  Returns store.ErrNotFound when the thread is not owned
// by the user.
func (s *Service) GetThreadForUser(ctx context.Context, userID, threadID string) ([]store.ThreadMessage, error) {
	return s.store.GetThreadForUser(ctx, userID, threadID)
}

// DeleteThreadForUser removes a thread owned by userID.  Returns
// store.ErrNotFound when the thread is not owned by the user.
func (s *Service) DeleteThreadForUser(ctx context.Context, userID, threadID string) error {
	return s.store.DeleteThreadForUser(ctx, userID, threadID)
}

// RenameThreadForUser updates the human-readable title for a thread owned by
// userID.  Returns store.ErrNotFound when the thread is not owned by the user.
func (s *Service) RenameThreadForUser(ctx context.Context, userID, threadID, title string) error {
	return s.store.RenameThreadForUser(ctx, userID, threadID, title)
}

// recordRunMetrics updates in-process counters after a run finishes.
// It is a no-op when the service was created without a Metrics instance.
func (s *Service) recordRunMetrics(run *store.Run, latency time.Duration) {
	if s.metrics == nil {
		return
	}
	s.metrics.RecordRunCompleted(latency.Milliseconds())
	s.metrics.ToolCallsTotal.Add(int64(len(run.ToolCalls)))
	s.metrics.ApprovalRequestsTotal.Add(int64(len(run.ApprovalRecs)))
	if run.ModelBackend != "" {
		s.metrics.BackendSelectionsTotal.Add(1)
	}
}

// buildRunPolicy converts a ToolPolicySpec from the caller into an
// AllowlistPolicy. Returns nil when the spec is nil or has no restrictions.
func buildRunPolicy(spec *ToolPolicySpec) policy.Policy {
	if spec == nil {
		return nil
	}
	if len(spec.AllowedTools) == 0 && len(spec.RequireApproval) == 0 && len(spec.DeniedTools) == 0 {
		return nil
	}
	approvalMap := make(map[string]bool, len(spec.RequireApproval))
	for _, t := range spec.RequireApproval {
		approvalMap[t] = true
	}
	return &policy.AllowlistPolicy{
		AllowedToolNames: spec.AllowedTools,
		DeniedToolNames:  spec.DeniedTools,
		ApprovalTools:    approvalMap,
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
