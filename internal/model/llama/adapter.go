package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
)

type Adapter struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func New(baseURL string) *Adapter {
	return &Adapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func NewWithAPIKey(baseURL, apiKey string) *Adapter {
	adapter := New(baseURL)
	adapter.apiKey = strings.TrimSpace(apiKey)
	return adapter
}

// ── request types ─────────────────────────────────────────────────────────────

type chatRequest struct {
	Model         string            `json:"model"`
	Messages      []llamaRequestMsg `json:"messages"`
	MaxTokens     int               `json:"max_tokens"`
	Stream        bool              `json:"stream"`
	StreamOptions *streamOptions    `json:"stream_options,omitempty"`
	Tools         []llamaToolDef    `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// llamaToolDef is the OpenAI-compatible function/tool advertisement format.
type llamaToolDef struct {
	Type     string           `json:"type"` // always "function"
	Function llamaToolDefBody `json:"function"`
}

type llamaToolDefBody struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

func toRequestTools(specs []model.ToolSpec) []llamaToolDef {
	if len(specs) == 0 {
		return nil
	}
	out := make([]llamaToolDef, len(specs))
	for i, s := range specs {
		params := s.Parameters
		if params == nil {
			// llama-server requires an object schema; emit an empty object.
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out[i] = llamaToolDef{
			Type: "function",
			Function: llamaToolDefBody{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  params,
			},
		}
	}
	return out
}

// llamaRequestMsg is the OpenAI-compatible message format used in requests.
// Tool-call messages and tool-result messages require special fields that are
// absent from the simpler model.Message type.
type llamaRequestMsg struct {
	Role       string             `json:"role"`
	Content    *string            `json:"content"` // nullable for tool-call assistant turns
	ToolCalls  []llamaToolCallOut `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

// llamaToolCallOut serialises a model.ToolCall into the OpenAI wire format.
type llamaToolCallOut struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

// toRequestMsg converts a model.Message to the OpenAI wire format expected by
// the llama.cpp / OpenAI-compatible API.
func toRequestMsg(m model.Message) llamaRequestMsg {
	msg := llamaRequestMsg{Role: string(m.Role)}

	if len(m.ToolCalls) > 0 {
		// Assistant message that carries tool-call requests – content may be null.
		if m.Content != "" {
			c := m.Content
			msg.Content = &c
		}
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Params)
			out := llamaToolCallOut{ID: tc.ID, Type: "function"}
			out.Function.Name = tc.Name
			out.Function.Arguments = string(argsJSON)
			msg.ToolCalls = append(msg.ToolCalls, out)
		}
	} else if m.ToolCallID != "" {
		// Tool-result message.
		c := m.Content
		msg.Content = &c
		msg.ToolCallID = m.ToolCallID
	} else {
		// Regular user / system / assistant message.
		c := m.Content
		msg.Content = &c
	}
	return msg
}

// ── response types ─────────────────────────────────────────────────────────────

// llamaResponseMsg is the message shape inside a non-streaming completion.
type llamaResponseMsg struct {
	Role             string            `json:"role"`
	Content          *string           `json:"content"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []llamaToolCallIn `json:"tool_calls,omitempty"`
}

// llamaToolCallIn is the tool-call shape in an API response.
type llamaToolCallIn struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

type chatResponse struct {
	Choices []struct {
		Message      llamaResponseMsg `json:"message"`
		FinishReason string           `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string                  `json:"content"`
			ReasoningContent string                  `json:"reasoning_content,omitempty"`
			ToolCalls        []llamaStreamToolCallIn `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

type llamaStreamToolCallIn struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// ── Provider implementation ───────────────────────────────────────────────────

func (a *Adapter) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	msgs := make([]llamaRequestMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = toRequestMsg(m)
	}

	modelName := req.Model
	if modelName == "" {
		modelName = "local"
	}
	body, err := json.Marshal(chatRequest{
		Model:     modelName,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
		Stream:    false,
		Tools:     toRequestTools(req.Tools),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama: unexpected status %d: %s", resp.StatusCode, b)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llama: no choices in response")
	}

	choice := cr.Choices[0]
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
	}

	modelResp := &model.Response{
		Content:      content,
		FinishReason: choice.FinishReason,
		Usage: model.Usage{
			PromptTokens:     cr.Usage.PromptTokens,
			CompletionTokens: cr.Usage.CompletionTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		},
	}

	// Convert any tool calls from the OpenAI wire format to model.ToolCall.
	for _, tc := range choice.Message.ToolCalls {
		var params map[string]any
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &params)
		}
		modelResp.ToolCalls = append(modelResp.ToolCalls, model.ToolCall{
			ID:     tc.ID,
			Name:   tc.Function.Name,
			Params: params,
		})
	}

	return modelResp, nil
}

func (a *Adapter) Stream(ctx context.Context, req model.Request, onChunk func(chunk string) error) error {
	_, err := a.StreamComplete(ctx, req, onChunk)
	return err
}

func (a *Adapter) StreamComplete(ctx context.Context, req model.Request, onChunk func(chunk string) error) (*model.Response, error) {
	return a.StreamCompleteWithReasoning(ctx, req, onChunk, nil)
}

func (a *Adapter) StreamCompleteWithReasoning(ctx context.Context, req model.Request, onChunk func(chunk string) error, onReasoning func(chunk string) error) (*model.Response, error) {
	msgs := make([]llamaRequestMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = toRequestMsg(m)
	}

	streamModelName := req.Model
	if streamModelName == "" {
		streamModelName = "local"
	}
	body, err := json.Marshal(chatRequest{
		Model:         streamModelName,
		Messages:      msgs,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Tools:         toRequestTools(req.Tools),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama: unexpected status %d: %s", resp.StatusCode, b)
	}

	var content strings.Builder
	finishReason := ""
	toolCalls := map[int]*llamaToolCallIn{}
	usage := model.Usage{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}
		if chunk.Usage != nil {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
			usage.TotalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
		if reasoningDelta := choice.Delta.ReasoningContent; reasoningDelta != "" {
			if onReasoning != nil {
				if err := onReasoning(reasoningDelta); err != nil {
					return nil, err
				}
			}
		}
		if delta := choice.Delta.Content; delta != "" {
			content.WriteString(delta)
			if onChunk != nil {
				if err := onChunk(delta); err != nil {
					return nil, err
				}
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			call := toolCalls[tc.Index]
			if call == nil {
				call = &llamaToolCallIn{}
				toolCalls[tc.Index] = call
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Type != "" {
				call.Type = tc.Type
			}
			if tc.Function.Name != "" {
				call.Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				call.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	modelResp := &model.Response{
		Content:      content.String(),
		FinishReason: finishReason,
		Usage:        usage,
	}
	for i := 0; i < len(toolCalls); i++ {
		call := toolCalls[i]
		if call == nil {
			continue
		}
		var params map[string]any
		if call.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(call.Function.Arguments), &params)
		}
		modelResp.ToolCalls = append(modelResp.ToolCalls, model.ToolCall{
			ID:     call.ID,
			Name:   call.Function.Name,
			Params: params,
		})
	}
	return modelResp, nil
}

func (a *Adapter) chatCompletionsURL() string {
	if strings.HasSuffix(a.baseURL, "/v1") {
		return a.baseURL + "/chat/completions"
	}
	return a.baseURL + "/v1/chat/completions"
}
