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
client  *http.Client
}

func New(baseURL string) *Adapter {
return &Adapter{
baseURL: baseURL,
client:  &http.Client{Timeout: 60 * time.Second},
}
}

// ── request types ─────────────────────────────────────────────────────────────

type chatRequest struct {
Model     string               `json:"model"`
Messages  []llamaRequestMsg    `json:"messages"`
MaxTokens int                  `json:"max_tokens"`
Stream    bool                 `json:"stream"`
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
Role      string             `json:"role"`
Content   *string            `json:"content"`
ToolCalls []llamaToolCallIn  `json:"tool_calls,omitempty"`
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
}

type streamChunk struct {
Choices []struct {
Delta        struct{ Content string `json:"content"` } `json:"delta"`
FinishReason *string                                   `json:"finish_reason"`
} `json:"choices"`
}

// ── Provider implementation ───────────────────────────────────────────────────

func (a *Adapter) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
msgs := make([]llamaRequestMsg, len(req.Messages))
for i, m := range req.Messages {
msgs[i] = toRequestMsg(m)
}

body, err := json.Marshal(chatRequest{
Model:     "local",
Messages:  msgs,
MaxTokens: req.MaxTokens,
Stream:    false,
})
if err != nil {
return nil, err
}

httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
if err != nil {
return nil, err
}
httpReq.Header.Set("Content-Type", "application/json")

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
msgs := make([]llamaRequestMsg, len(req.Messages))
for i, m := range req.Messages {
msgs[i] = toRequestMsg(m)
}

body, err := json.Marshal(chatRequest{
Model:     "local",
Messages:  msgs,
MaxTokens: req.MaxTokens,
Stream:    true,
})
if err != nil {
return err
}

httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
if err != nil {
return err
}
httpReq.Header.Set("Content-Type", "application/json")

resp, err := a.client.Do(httpReq)
if err != nil {
return err
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
b, _ := io.ReadAll(resp.Body)
return fmt.Errorf("llama: unexpected status %d: %s", resp.StatusCode, b)
}

scanner := bufio.NewScanner(resp.Body)
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
return err
}
if len(chunk.Choices) == 0 {
continue
}
if content := chunk.Choices[0].Delta.Content; content != "" {
if err := onChunk(content); err != nil {
return err
}
}
}
return scanner.Err()
}
