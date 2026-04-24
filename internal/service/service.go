package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
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
}

type Service struct {
	store     store.Store
	agent     *agent.Agent
	approvals *policy.Approvals
	metrics   *metrics.Metrics
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
		}),
		approvals: approvals,
		metrics:   opts.Metrics,
	}
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
		s.recordRunMetrics(run, time.Since(start))
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.Response = fmt.Sprintf("Completed processing: %s", prompt)
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	s.recordRunMetrics(run, time.Since(start))
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
