package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

type Postgres struct {
	db *sql.DB
}

func NewPostgres(db *sql.DB) *Postgres {
	return &Postgres{db: db}
}

func (p *Postgres) CreateSession(ctx context.Context, s *Session) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO sessions (id, name, description, created_at) VALUES ($1, $2, $3, $4)`,
		s.ID, s.Name, s.Description, s.CreatedAt,
	)
	return err
}

func (p *Postgres) GetSession(ctx context.Context, id string) (*Session, error) {
	s := &Session{}
	err := p.db.QueryRowContext(ctx,
		`SELECT id, name, description, created_at FROM sessions WHERE id = $1`, id,
	).Scan(&s.ID, &s.Name, &s.Description, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Postgres) CreateRun(ctx context.Context, r *Run) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, prompt, status, response, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.ID, r.SessionID, r.Prompt, r.Status, r.Response, r.CreatedAt, r.UpdatedAt,
	)
	return err
}

func (p *Postgres) GetRun(ctx context.Context, id string) (*Run, error) {
	r := &Run{}
	err := p.db.QueryRowContext(ctx,
		`SELECT id, session_id, prompt, status, response, created_at, updated_at FROM runs WHERE id = $1`, id,
	).Scan(&r.ID, &r.SessionID, &r.Prompt, &r.Status, &r.Response, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (p *Postgres) UpdateRun(ctx context.Context, r *Run) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE runs SET status = $1, response = $2, updated_at = $3 WHERE id = $4`,
		r.Status, r.Response, r.UpdatedAt, r.ID,
	)
	return err
}
