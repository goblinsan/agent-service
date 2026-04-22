package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

type Service struct {
	store store.Store
}

func New(s store.Store) *Service {
	return &Service{store: s}
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

	for i := 1; i <= 3; i++ {
		if err := sse.Write(w, sse.Event{Type: "run.step", Data: map[string]interface{}{
			"step":    i,
			"message": fmt.Sprintf("processing step %d", i),
		}}); err != nil {
			return err
		}
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
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
