package store

import (
	"context"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
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
	// BackendNode, when non-empty, pins the run to a specific registered
	// llm-service node by name (e.g. "papai").  Takes precedence over
	// ModelBackend in multi-node Pools.  Persisted as a transient field
	// only; not currently stored in the database.
	BackendNode string
	// BackendBaseURL and BackendAPIKey are transient per-run endpoint overrides.
	// BackendAPIKey is sensitive and must never be persisted or logged.
	BackendBaseURL string
	BackendAPIKey  string
	// ToolCalls holds every tool invocation made during the run.
	ToolCalls []RunToolCall
	// ApprovalRecs records the human-approval outcomes for the run.
	ApprovalRecs []RunApprovalRecord
	// PausedState is an opaque JSON blob used to resume a paused run.
	PausedState string
	// Usage stores aggregate token accounting across all model calls in the run.
	Usage model.Usage
	// CompletionTokensPerSecond is the observed assistant-generation throughput
	// for the final response, when it can be measured.
	CompletionTokensPerSecond float64 `json:"completion_tokens_per_second,omitempty"`
	// CompletionElapsedMS is the elapsed generation time for the final response.
	CompletionElapsedMS int64 `json:"completion_elapsed_ms,omitempty"`
	// StructuredOutput stores parsed automation output when the caller requested
	// a structured sync response (for example, Kulrs palette analysis JSON).
	StructuredOutput any

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

// UserMemory is a durable per-user key/value fact surfaced to the model on each
// turn. Confidence is informational (1.0 = stated by user, lower for inferred).
type UserMemory struct {
	Key        string
	Value      string
	Confidence float64
	UpdatedAt  time.Time
}

// UserProfile is an editable structured view over profile.* durable memories.
// It lets clients expose core facts without introducing a separate migration.
type UserProfile struct {
	UserID    string               `json:"user_id"`
	Sections  []UserProfileSection `json:"sections"`
	UpdatedAt *time.Time           `json:"updated_at,omitempty"`
}

type UserProfileSection struct {
	ID     string             `json:"id"`
	Title  string             `json:"title"`
	Fields []UserProfileField `json:"fields"`
}

type UserProfileField struct {
	Key       string     `json:"key"`
	Label     string     `json:"label"`
	Value     string     `json:"value"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// UserEvent is an append-only log entry describing something the user did or
// said. Kind is a coarse label (e.g. "chat", "tool", "plan"). Source identifies
// the producer (e.g. "chat-platform", "agent-service").
type UserEvent struct {
	ID        int64
	UserID    string
	SessionID string
	Kind      string
	Source    string
	Summary   string
	Payload   map[string]any
	CreatedAt time.Time
}

// UserPlan is a durable goal/plan attached to a user, with optional structured
// steps. Status is a free-form label ("draft", "active", "done", "abandoned").
type UserPlan struct {
	ID                 string                  `json:"id"`
	UserID             string                  `json:"user_id"`
	Title              string                  `json:"title"`
	Status             string                  `json:"status"`
	Vision             string                  `json:"vision"`
	Target             string                  `json:"target"`
	Category           string                  `json:"category"`
	Objectives         []string                `json:"objectives"`
	Principles         []string                `json:"principles"`
	Tags               []string                `json:"tags"`
	DataSources        []string                `json:"data_sources"`
	Connectors         []PlanConnector         `json:"connectors"`
	ReviewCadence      string                  `json:"review_cadence"`
	Summary            string                  `json:"summary"`
	Metrics            map[string]any          `json:"metrics"`
	TrackedMetrics     []PlanTrackedMetric     `json:"tracked_metrics"`
	BaselineFacts      []PlanFact              `json:"baseline_facts"`
	SuccessCriteria    []string                `json:"success_criteria"`
	Cadence            []PlanCadenceEntry      `json:"cadence"`
	SupportingSections []PlanSupportingSection `json:"supporting_sections"`
	Milestones         []UserPlanMilestone     `json:"milestones"`
	Progress           UserPlanProgress        `json:"progress"`
	Steps              []map[string]any        `json:"steps"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
}

type PlanFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type PlanTrackedMetric struct {
	Name     string `json:"name"`
	Notes    string `json:"notes"`
	Source   string `json:"source"`
	Cadence  string `json:"cadence"`
	Baseline string `json:"baseline"`
	Target   string `json:"target"`
}

type PlanCadenceEntry struct {
	Label    string `json:"label"`
	Day      string `json:"day"`
	Activity string `json:"activity"`
	Notes    string `json:"notes"`
}

type PlanSupportingSection struct {
	Title   string               `json:"title"`
	Kind    string               `json:"kind"`
	Summary string               `json:"summary"`
	Items   []PlanSupportingItem `json:"items"`
}

type PlanSupportingItem struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	URI     string `json:"uri"`
}

type UserPlanMilestone struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Status        string         `json:"status"`
	Summary       string         `json:"summary"`
	ScheduledDate *time.Time     `json:"scheduled_date,omitempty"`
	StartDate     *time.Time     `json:"start_date,omitempty"`
	TargetDate    *time.Time     `json:"target_date,omitempty"`
	EndDate       *time.Time     `json:"end_date,omitempty"`
	DependsOn     []string       `json:"depends_on,omitempty"`
	Sequence      int            `json:"sequence,omitempty"`
	Tasks         []UserPlanTask `json:"tasks"`
}

type UserPlanTask struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Notes       string     `json:"notes"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	StartAt     *time.Time `json:"start_at,omitempty"`
	TargetAt    *time.Time `json:"target_at,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	EndAt       *time.Time `json:"end_at,omitempty"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	Sequence    int        `json:"sequence,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type UserPlanProgress struct {
	MilestoneCount      int        `json:"milestone_count"`
	CompletedMilestones int        `json:"completed_milestones"`
	TaskCount           int        `json:"task_count"`
	CompletedTasks      int        `json:"completed_tasks"`
	PercentComplete     float64    `json:"percent_complete"`
	IsStale             bool       `json:"is_stale"`
	NextReviewAt        *time.Time `json:"next_review_at,omitempty"`
}

// PlanConnector describes an external personal-data app that contributes to a
// durable plan (for example Apple Health, Strava, or Lose It).
type PlanConnector struct {
	App        string `json:"app" yaml:"app"`
	Type       string `json:"type,omitempty" yaml:"type,omitempty"`
	Domain     string `json:"domain,omitempty" yaml:"domain,omitempty"`
	ExternalID string `json:"external_id,omitempty" yaml:"external_id,omitempty"`
}

// ThreadSummary is a compact description of a chat thread (session) belonging
// to a user, suitable for rendering in a thread list / sidebar.
type ThreadSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	LastSnippet  string    `json:"last_snippet,omitempty"`
	LastAgentID  string    `json:"last_agent_id,omitempty"`
}

// ThreadMessage is a single user/assistant message reconstructed from a run
// row when streaming back the contents of a thread.
type ThreadMessage struct {
	Role      string    `json:"role"` // "user" | "assistant"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	RunID     string    `json:"run_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
}

// ScheduledJob is a durable scheduled task owned by a user.
type ScheduledJob struct {
	ID          string
	UserID      string
	Kind        string
	Prompt      string
	ThreadID    string
	AgentID     string
	Payload     map[string]any
	RunAt       time.Time
	Recurrence  string
	Timezone    string
	Status      string
	LockedUntil *time.Time
	LastRunAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ScheduledJobPatch updates mutable schedule fields. A nil field is left
// unchanged; an empty string pointer clears nullable string fields.
type ScheduledJobPatch struct {
	Kind       *string
	Prompt     *string
	ThreadID   *string
	AgentID    *string
	Payload    map[string]any
	PayloadSet bool
	RunAt      *time.Time
	Recurrence *string
	Timezone   *string
	Status     *string
	ClearLock  bool
}

// Notification is an inbox item generated by agent-service.
type Notification struct {
	ID          string
	UserID      string
	Kind        string
	Title       string
	Body        string
	ThreadID    string
	SourceRunID string
	Payload     map[string]any
	ReadAt      *time.Time
	DismissedAt *time.Time
	CreatedAt   time.Time
}

// DeviceToken stores push-delivery registration metadata.
type DeviceToken struct {
	ID         string
	UserID     string
	Platform   string
	Token      string
	AppVersion string
	LastSeenAt time.Time
	CreatedAt  time.Time
}

type Store interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	CreateRun(ctx context.Context, r *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	UpdateRun(ctx context.Context, r *Run) error
	CreateStep(ctx context.Context, step *RunStep) error
	ListSteps(ctx context.Context, runID string) ([]*RunStep, error)

	// Per-user thread (session) browsing for chat clients.
	ListThreadsForUser(ctx context.Context, userID string, limit int) ([]ThreadSummary, error)
	GetThreadForUser(ctx context.Context, userID, threadID string) ([]ThreadMessage, error)
	DeleteThreadForUser(ctx context.Context, userID, threadID string) error
	RenameThreadForUser(ctx context.Context, userID, threadID, title string) error

	// Per-user identity, memory, event log, and plans.
	EnsureUser(ctx context.Context, id, displayName string) error
	ListUserMemories(ctx context.Context, userID string) ([]UserMemory, error)
	UpsertUserMemory(ctx context.Context, userID, key, value string, confidence float64) error
	AppendUserEvent(ctx context.Context, evt *UserEvent) error
	ListRecentUserEvents(ctx context.Context, userID string, limit int) ([]UserEvent, error)
	UpsertUserPlan(ctx context.Context, p *UserPlan) error
	ListActivePlans(ctx context.Context, userID string) ([]UserPlan, error)
	GetUserPlan(ctx context.Context, userID, planID string) (*UserPlan, error)
	DeleteUserPlan(ctx context.Context, userID, planID string) error

	// Per-user notifications inbox.
	CreateNotification(ctx context.Context, n *Notification) error
	ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]Notification, error)
	MarkNotificationRead(ctx context.Context, userID, notificationID string) error
	MarkAllNotificationsRead(ctx context.Context, userID string) error
	DeleteNotification(ctx context.Context, userID, notificationID string) error

	// Scheduled jobs and scheduler worker support.
	CreateScheduledJob(ctx context.Context, job *ScheduledJob) error
	ListScheduledJobs(ctx context.Context, userID string, limit int) ([]ScheduledJob, error)
	ListScheduledJobHistory(ctx context.Context, userID string, limit int) ([]ScheduledJob, error)
	PatchScheduledJob(ctx context.Context, userID, jobID string, patch ScheduledJobPatch) error
	DeleteScheduledJob(ctx context.Context, userID, jobID string) error
	AcquireDueScheduledJobs(ctx context.Context, limit int, lease time.Duration) ([]ScheduledJob, error)
	MarkScheduledJobResult(ctx context.Context, jobID, status string, lastRunAt time.Time, nextRunAt *time.Time) error

	// Push-device registration.
	UpsertDeviceToken(ctx context.Context, token *DeviceToken) error
	DeleteDeviceToken(ctx context.Context, userID, token string) error
	ListDeviceTokens(ctx context.Context, userID, platform string) ([]DeviceToken, error)
}
