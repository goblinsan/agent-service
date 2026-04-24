package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Run lifecycle event type constants.
// These are emitted on the SSE stream for every run, regardless of caller type,
// so gateway-chat-platform and automation consumers share a single event vocabulary.
const (
	// EventRunCreated fires when a run record has been persisted.
	EventRunCreated = "run.created"
	// EventRunInProgress fires when the agent has begun executing.
	EventRunInProgress = "run.in_progress"
	// EventRunModelSelected fires when a model backend has been chosen for the run.
	EventRunModelSelected = "run.model_selected"
	// EventRunToolCall fires each time the agent invokes a tool.
	EventRunToolCall = "run.tool_call"
	// EventRunApprovalRequested fires when a tool call requires human approval.
	EventRunApprovalRequested = "run.approval_requested"
	// EventRunAssistantDelta fires for each streamed assistant output chunk.
	EventRunAssistantDelta = "run.assistant_delta"
	// EventRunStep fires for each completed agent step.
	EventRunStep = "run.step"
	// EventRunPaused fires when a run is suspended pending an external decision.
	EventRunPaused = "run.paused"
	// EventRunCompleted fires when the run has finished successfully.
	EventRunCompleted = "run.completed"
	// EventRunFailed fires when the run has terminated with an error.
	EventRunFailed = "run.failed"
)

// ModelSelectedPayload is the data payload for a run.model_selected event.
type ModelSelectedPayload struct {
	RunID   string `json:"run_id"`
	Backend string `json:"backend"`
}

// ToolCallPayload is the data payload for a run.tool_call event.
type ToolCallPayload struct {
	RunID    string         `json:"run_id"`
	ToolName string         `json:"tool_name"`
	Params   map[string]any `json:"params"`
	Result   string         `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// ApprovalRequestedPayload is the data payload for a run.approval_requested event.
type ApprovalRequestedPayload struct {
	RunID      string         `json:"run_id"`
	ApprovalID string         `json:"approval_id"`
	ToolName   string         `json:"tool_name"`
	Params     map[string]any `json:"params"`
}

// AssistantDeltaPayload is the data payload for a run.assistant_delta event.
type AssistantDeltaPayload struct {
	RunID string `json:"run_id"`
	Delta string `json:"delta"`
}

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func Write(w http.ResponseWriter, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
