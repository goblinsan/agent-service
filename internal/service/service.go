package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/goblinsan/agent-service/internal/agent"
	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

type Service struct {
	store     store.Store
	agent     *agent.Agent
	approvals *policy.Approvals
}

func New(s store.Store, p model.Provider, maxSteps int) *Service {
	return &Service{
		store:     s,
		agent:     agent.New(p, s, maxSteps),
		approvals: policy.NewApprovals(),
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

	if err := s.agent.Run(ctx, run, w); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		_ = sse.Write(w, sse.Event{Type: sse.EventRunFailed, Data: run})
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.Response = fmt.Sprintf("Completed processing: %s", prompt)
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
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

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
