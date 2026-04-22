package store

import (
	"context"
	"time"
)

type Session struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

type Run struct {
	ID        string
	SessionID string
	Prompt    string
	Status    string
	Response  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	CreateRun(ctx context.Context, r *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	UpdateRun(ctx context.Context, r *Run) error
}
