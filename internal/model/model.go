package model

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool is used for messages that carry the result of a tool execution.
	RoleTool Role = "tool"
)

// ToolCall represents a function/tool call requested by the model.
type ToolCall struct {
	// ID is a unique identifier for this call assigned by the model.
	ID string `json:"id"`
	// Name is the tool name to invoke.
	Name string `json:"name"`
	// Params holds the parsed arguments for the call.
	Params map[string]any `json:"params,omitempty"`
}

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is non-empty when an assistant message contains tool call requests.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool-result message back to the originating ToolCall.ID.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Request struct {
	Model     string
	Messages  []Message
	MaxTokens int
	// EstimatedPromptTokens is an approximate caller-provided token count for
	// the input messages, used for routing decisions in multi-node pools.
	EstimatedPromptTokens int
	Stream                bool
}

type Response struct {
	Content      string
	FinishReason string
	// ToolCalls contains any tool calls the model requested in this response.
	ToolCalls []ToolCall
	Usage     Usage
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request, onChunk func(chunk string) error) error
}
