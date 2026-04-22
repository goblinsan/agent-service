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
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

type Service struct {
	store store.Store
	agent *agent.Agent
}

func New(s store.Store, p model.Provider) *Service {
	return &Service{store: s, agent: agent.New(p, s, 10)}
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
		Prompt:    prompt,
		Status:    "created",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if err := sse.Write(w, sse.Event{Type: "run.created", Data: run}); err != nil {
		return err
	}

	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run in_progress: %w", err)
	}
	if err := sse.Write(w, sse.Event{Type: "run.in_progress", Data: run}); err != nil {
		return err
	}

	if err := s.agent.Run(ctx, run, w); err != nil {
		run.Status = "failed"
		run.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateRun(ctx, run)
		_ = sse.Write(w, sse.Event{Type: "run.failed", Data: run})
		return fmt.Errorf("agent run: %w", err)
	}

	run.Status = "completed"
	run.Response = fmt.Sprintf("Completed processing: %s", prompt)
	run.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("update run completed: %w", err)
	}
	return sse.Write(w, sse.Event{Type: "run.completed", Data: run})
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
