package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	// Ensure nil slices marshal as "[]" rather than "null".
	toolCalls := r.ToolCalls
	if toolCalls == nil {
		toolCalls = []RunToolCall{}
	}
	approvalRecs := r.ApprovalRecs
	if approvalRecs == nil {
		approvalRecs = []RunApprovalRecord{}
	}

	toolCallsJSON, err := json.Marshal(toolCalls)
	if err != nil {
		return err
	}
	approvalRecsJSON, err := json.Marshal(approvalRecs)
	if err != nil {
		return err
	}

	var sessionID *string
	if r.SessionID != "" {
		sessionID = &r.SessionID
	}

	_, err = p.db.ExecContext(ctx,
		`INSERT INTO runs (
			id, session_id, source, prompt, status, response,
			model_backend, tool_calls, approval_records, paused_state,
			request_id, thread_id, user_id, agent_id,
			workflow_id, job_type,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14,
			$15, $16,
			$17, $18
		)`,
		r.ID, sessionID, r.Source, r.Prompt, r.Status, r.Response,
		r.ModelBackend, string(toolCallsJSON), string(approvalRecsJSON), nullableString(r.PausedState),
		nullableString(r.RequestID), nullableString(r.ThreadID), nullableString(r.UserID), nullableString(r.AgentID),
		nullableString(r.WorkflowID), nullableString(r.JobType),
		r.CreatedAt, r.UpdatedAt,
	)
	return err
}

func (p *Postgres) GetRun(ctx context.Context, id string) (*Run, error) {
	r := &Run{}
	var sessionID sql.NullString
	var toolCallsJSON, approvalRecsJSON string
	var modelBackend, pausedState, requestID, threadID, userID, agentID, workflowID, jobType sql.NullString

	err := p.db.QueryRowContext(ctx,
		`SELECT id, session_id, source, prompt, status, response,
			model_backend, tool_calls, approval_records, paused_state,
			request_id, thread_id, user_id, agent_id,
			workflow_id, job_type,
			created_at, updated_at
		FROM runs WHERE id = $1`, id,
	).Scan(
		&r.ID, &sessionID, &r.Source, &r.Prompt, &r.Status, &r.Response,
		&modelBackend, &toolCallsJSON, &approvalRecsJSON, &pausedState,
		&requestID, &threadID, &userID, &agentID,
		&workflowID, &jobType,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	r.SessionID = sessionID.String
	r.ModelBackend = modelBackend.String
	r.PausedState = pausedState.String
	r.RequestID = requestID.String
	r.ThreadID = threadID.String
	r.UserID = userID.String
	r.AgentID = agentID.String
	r.WorkflowID = workflowID.String
	r.JobType = jobType.String

	if toolCallsJSON != "" {
		if err := json.Unmarshal([]byte(toolCallsJSON), &r.ToolCalls); err != nil {
			return nil, err
		}
	}
	if approvalRecsJSON != "" {
		if err := json.Unmarshal([]byte(approvalRecsJSON), &r.ApprovalRecs); err != nil {
			return nil, err
		}
	}

	return r, nil
}

func (p *Postgres) UpdateRun(ctx context.Context, r *Run) error {
	// Ensure nil slices marshal as "[]" rather than "null".
	toolCalls := r.ToolCalls
	if toolCalls == nil {
		toolCalls = []RunToolCall{}
	}
	approvalRecs := r.ApprovalRecs
	if approvalRecs == nil {
		approvalRecs = []RunApprovalRecord{}
	}

	toolCallsJSON, err := json.Marshal(toolCalls)
	if err != nil {
		return err
	}
	approvalRecsJSON, err := json.Marshal(approvalRecs)
	if err != nil {
		return err
	}

	res, err := p.db.ExecContext(ctx,
		`UPDATE runs SET
			status = $1, response = $2, updated_at = $3,
			model_backend = $4, tool_calls = $5, approval_records = $6, paused_state = $7
		WHERE id = $8`,
		r.Status, r.Response, r.UpdatedAt,
		nullableString(r.ModelBackend), string(toolCallsJSON), string(approvalRecsJSON), nullableString(r.PausedState),
		r.ID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreateStep(ctx context.Context, step *RunStep) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO run_steps (id, run_id, step_index, role, content, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		step.ID, step.RunID, step.Index, step.Role, step.Content, step.CreatedAt,
	)
	return err
}

func (p *Postgres) ListSteps(ctx context.Context, runID string) ([]*RunStep, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, run_id, step_index, role, content, created_at FROM run_steps WHERE run_id = $1 ORDER BY step_index`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []*RunStep
	for rows.Next() {
		s := &RunStep{}
		if err := rows.Scan(&s.ID, &s.RunID, &s.Index, &s.Role, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// nullableString converts an empty string to a sql.NullString with Valid=false,
// so that empty context IDs are stored as NULL rather than empty strings.
func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
