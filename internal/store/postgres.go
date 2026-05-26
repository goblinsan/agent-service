package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
	structuredOutputJSON, err := marshalStructuredOutput(r.StructuredOutput)
	if err != nil {
		return err
	}

	var sessionID *string
	if r.SessionID != "" {
		sessionID = &r.SessionID
		// Auto-create a placeholder session row when callers (e.g.
		// gateway-chat-platform) pass a thread_id without first POSTing
		// /sessions, so the runs.session_id FK is always satisfied.
		if _, err := p.db.ExecContext(ctx,
			`INSERT INTO sessions (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
			r.SessionID, "auto",
		); err != nil {
			return err
		}
	}

	_, err = p.db.ExecContext(ctx,
		`INSERT INTO runs (
			id, session_id, source, prompt, status, response,
			model_backend, tool_calls, approval_records, paused_state, structured_output,
			prompt_tokens, completion_tokens, total_tokens,
			request_id, thread_id, user_id, agent_id,
			workflow_id, job_type,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14,
			$15, $16, $17, $18,
			$19, $20,
			$21, $22
		)`,
		r.ID, sessionID, r.Source, r.Prompt, r.Status, r.Response,
		r.ModelBackend, string(toolCallsJSON), string(approvalRecsJSON), nullableString(r.PausedState), structuredOutputJSON,
		nullableInt(r.Usage.PromptTokens), nullableInt(r.Usage.CompletionTokens), nullableInt(r.Usage.TotalTokens),
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
	var structuredOutputJSON sql.NullString
	var promptTokens, completionTokens, totalTokens sql.NullInt64

	err := p.db.QueryRowContext(ctx,
		`SELECT id, session_id, source, prompt, status, response,
			model_backend, tool_calls, approval_records, paused_state, structured_output,
			prompt_tokens, completion_tokens, total_tokens,
			request_id, thread_id, user_id, agent_id,
			workflow_id, job_type,
			created_at, updated_at
		FROM runs WHERE id = $1`, id,
	).Scan(
		&r.ID, &sessionID, &r.Source, &r.Prompt, &r.Status, &r.Response,
		&modelBackend, &toolCallsJSON, &approvalRecsJSON, &pausedState, &structuredOutputJSON,
		&promptTokens, &completionTokens, &totalTokens,
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
	r.Usage.PromptTokens = int(promptTokens.Int64)
	r.Usage.CompletionTokens = int(completionTokens.Int64)
	r.Usage.TotalTokens = int(totalTokens.Int64)

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
	if structuredOutputJSON.Valid && structuredOutputJSON.String != "" {
		if err := json.Unmarshal([]byte(structuredOutputJSON.String), &r.StructuredOutput); err != nil {
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
	structuredOutputJSON, err := marshalStructuredOutput(r.StructuredOutput)
	if err != nil {
		return err
	}

	res, err := p.db.ExecContext(ctx,
		`UPDATE runs SET
			status = $1, response = $2, updated_at = $3,
			model_backend = $4, tool_calls = $5, approval_records = $6, paused_state = $7, structured_output = $8,
			prompt_tokens = $9, completion_tokens = $10, total_tokens = $11
		WHERE id = $12`,
		r.Status, r.Response, r.UpdatedAt,
		nullableString(r.ModelBackend), string(toolCallsJSON), string(approvalRecsJSON), nullableString(r.PausedState), structuredOutputJSON,
		nullableInt(r.Usage.PromptTokens), nullableInt(r.Usage.CompletionTokens), nullableInt(r.Usage.TotalTokens),
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

func nullableInt(v int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(v), Valid: v > 0}
}

func marshalStructuredOutput(value any) (sql.NullString, error) {
	if value == nil {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

// ListThreadsForUser returns a summary of every chat thread (session) that has
// at least one run owned by userID, newest activity first.  Truncated to limit.
// Session.Name is exposed as Title; when Name is the auto-placeholder "auto"
// the first user prompt is used instead so the list is human-readable.
func (p *Postgres) ListThreadsForUser(ctx context.Context, userID string, limit int) ([]ThreadSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `
		WITH user_sessions AS (
			SELECT DISTINCT session_id
			FROM runs
			WHERE user_id = $1 AND session_id IS NOT NULL
		),
		agg AS (
			SELECT
				r.session_id,
				MAX(r.updated_at) AS last_updated,
				COUNT(*) AS run_count,
				(ARRAY_AGG(r.response  ORDER BY r.updated_at DESC) FILTER (WHERE r.response IS NOT NULL AND r.response <> ''))[1] AS last_response,
				(ARRAY_AGG(r.prompt    ORDER BY r.updated_at DESC))[1] AS last_prompt,
				(ARRAY_AGG(r.agent_id  ORDER BY r.updated_at DESC) FILTER (WHERE r.agent_id IS NOT NULL AND r.agent_id <> ''))[1] AS last_agent_id,
				(ARRAY_AGG(r.prompt    ORDER BY r.created_at ASC))[1]  AS first_prompt
			FROM runs r
			WHERE r.user_id = $1 AND r.session_id IS NOT NULL
			GROUP BY r.session_id
		)
		SELECT s.id, s.name, s.created_at, a.last_updated, a.run_count,
		       a.last_response, a.last_prompt, a.last_agent_id, a.first_prompt
		FROM agg a
		JOIN sessions s ON s.id = a.session_id
		WHERE s.id IN (SELECT session_id FROM user_sessions)
		ORDER BY a.last_updated DESC
		LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ThreadSummary, 0, 16)
	for rows.Next() {
		var t ThreadSummary
		var name sql.NullString
		var lastResp, lastPrompt, lastAgent, firstPrompt sql.NullString
		var runCount int
		if err := rows.Scan(
			&t.ID, &name, &t.CreatedAt, &t.UpdatedAt, &runCount,
			&lastResp, &lastPrompt, &lastAgent, &firstPrompt,
		); err != nil {
			return nil, err
		}
		// Each run is roughly one user message + one assistant message.
		t.MessageCount = runCount * 2
		t.LastAgentID = lastAgent.String

		// Title: prefer human-set session name, else first user prompt (trimmed).
		title := strings.TrimSpace(name.String)
		if title == "" || title == "auto" {
			title = strings.TrimSpace(firstPrompt.String)
		}
		t.Title = truncate(title, 80)
		if t.Title == "" {
			t.Title = "New chat"
		}

		snippet := strings.TrimSpace(lastResp.String)
		if snippet == "" {
			snippet = strings.TrimSpace(lastPrompt.String)
		}
		t.LastSnippet = truncate(snippet, 140)

		out = append(out, t)
	}
	return out, rows.Err()
}

// GetThreadForUser returns the ordered user/assistant message history for a
// thread, restricted to threads that have at least one run owned by userID.
// Each run row contributes a user message (prompt) followed by an assistant
// message (response, if any).  Returns ErrNotFound when the thread is not
// owned by the user or has no runs.
func (p *Postgres) GetThreadForUser(ctx context.Context, userID, threadID string) ([]ThreadMessage, error) {
	// Authorisation: confirm this user owns at least one run in the thread.
	var ownedRuns int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE session_id = $1 AND user_id = $2`,
		threadID, userID,
	).Scan(&ownedRuns); err != nil {
		return nil, err
	}
	if ownedRuns == 0 {
		return nil, ErrNotFound
	}

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, prompt, response, agent_id, created_at, updated_at
		FROM runs
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC`,
		threadID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ThreadMessage, 0, ownedRuns*2)
	for rows.Next() {
		var runID, prompt string
		var response, agentID sql.NullString
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&runID, &prompt, &response, &agentID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if prompt != "" {
			out = append(out, ThreadMessage{
				Role:      "user",
				Content:   prompt,
				CreatedAt: createdAt.Time,
				RunID:     runID,
				AgentID:   agentID.String,
			})
		}
		if response.Valid && strings.TrimSpace(response.String) != "" {
			ts := updatedAt.Time
			if ts.IsZero() {
				ts = createdAt.Time
			}
			out = append(out, ThreadMessage{
				Role:      "assistant",
				Content:   response.String,
				CreatedAt: ts,
				RunID:     runID,
				AgentID:   agentID.String,
			})
		}
	}
	return out, rows.Err()
}

// DeleteThreadForUser removes a thread and its runs, scoped to the requesting
// user.  Returns ErrNotFound when the thread does not belong to the user.
// Older databases may still have dependent run_steps rows without cascade
// cleanup, so this delete is performed explicitly in dependency order.
func (p *Postgres) DeleteThreadForUser(ctx context.Context, userID, threadID string) error {
	var ownedRuns int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE session_id = $1 AND user_id = $2`,
		threadID, userID,
	).Scan(&ownedRuns); err != nil {
		return err
	}
	if ownedRuns == 0 {
		return ErrNotFound
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM run_steps
		WHERE run_id IN (
			SELECT id FROM runs WHERE session_id = $1 AND user_id = $2
		)`,
		threadID, userID,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM runs WHERE session_id = $1 AND user_id = $2`,
		threadID, userID,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, threadID); err != nil {
		return err
	}

	return tx.Commit()
}

// RenameThreadForUser updates the session.name (thread title), scoped to the
// requesting user.  Returns ErrNotFound when the thread does not belong to
// the user.
func (p *Postgres) RenameThreadForUser(ctx context.Context, userID, threadID, title string) error {
	var ownedRuns int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE session_id = $1 AND user_id = $2`,
		threadID, userID,
	).Scan(&ownedRuns); err != nil {
		return err
	}
	if ownedRuns == 0 {
		return ErrNotFound
	}
	_, err := p.db.ExecContext(ctx,
		`UPDATE sessions SET name = $1 WHERE id = $2`,
		truncate(strings.TrimSpace(title), 200), threadID,
	)
	return err
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
