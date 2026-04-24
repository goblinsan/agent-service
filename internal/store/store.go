package store

import (
	"context"
	"time"
)

// RunSource identifies the caller type that initiated a run.
type RunSource string

const (
	// RunSourceChat identifies runs initiated by the gateway-chat-platform.
	RunSourceChat RunSource = "chat"
	// RunSourceAutomation identifies runs initiated by scheduled jobs, background
	// workers, or workflow engines.
	RunSourceAutomation RunSource = "automation"
)

// RunToolCall records a single tool invocation made during an agent run.
type RunToolCall struct {
	ID        string         `json:"id"`
	ToolName  string         `json:"tool_name"`
	Params    map[string]any `json:"params"`
	Result    string         `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// RunApprovalRecord captures the outcome of a human-approval gate within a run.
type RunApprovalRecord struct {
	ApprovalID string    `json:"approval_id"`
	ToolName   string    `json:"tool_name"`
	Decision   string    `json:"decision"` // "approved" | "denied"
	Reason     string    `json:"reason,omitempty"`
	DecidedAt  time.Time `json:"decided_at,omitempty"`
}

type Session struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

// Run is the durable orchestration record for a single agent invocation.
// It stores enough state for auditing, resumption, and event replay.
type Run struct {
	ID        string
	SessionID string // may be empty for automation runs
	// Source is "chat" or "automation"; see RunSource constants.
	Source   string
	Prompt   string
	Status   string
	Response string

	// ModelBackend is the name of the model backend selected for this run.
	ModelBackend string
	// ToolCalls holds every tool invocation made during the run.
	ToolCalls []RunToolCall
	// ApprovalRecs records the human-approval outcomes for the run.
	ApprovalRecs []RunApprovalRecord
	// PausedState is an opaque JSON blob used to resume a paused run.
	PausedState string

	// Chat-context fields (populated when Source == "chat").
	RequestID string
	ThreadID  string
	UserID    string
	AgentID   string

	// Automation-context fields (populated when Source == "automation").
	WorkflowID string
	JobType    string

	CreatedAt time.Time
	UpdatedAt time.Time
}

type RunStep struct {
	ID        string
	RunID     string
	Index     int
	Role      string
	Content   string
	CreatedAt time.Time
}

type Store interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	CreateRun(ctx context.Context, r *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	UpdateRun(ctx context.Context, r *Run) error
	CreateStep(ctx context.Context, step *RunStep) error
	ListSteps(ctx context.Context, runID string) ([]*RunStep, error)
}
