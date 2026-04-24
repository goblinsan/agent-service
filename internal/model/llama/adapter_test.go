package llama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/model/llama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]string{"role": "assistant", "content": "Hello!"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	resp, err := adapter.Complete(context.Background(), model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "Hi"}},
		MaxTokens: 512,
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestComplete_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	_, err := adapter.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Hi"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{"Hello", ", ", "world!"}
		for _, c := range chunks {
			data := map[string]interface{}{
				"choices": []map[string]interface{}{
					{"delta": map[string]string{"content": c}, "finish_reason": nil},
				},
			}
			b, _ := json.Marshal(data)
			w.Write([]byte("data: " + string(b) + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	var sb strings.Builder
	err := adapter.Stream(context.Background(), model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "Hi"}},
		MaxTokens: 512,
		Stream:    true,
	}, func(chunk string) error {
		sb.WriteString(chunk)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello, world!", sb.String())
}

func TestStream_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	err := adapter.Stream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Hi"}},
	}, func(chunk string) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestComplete_WithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call-1",
								"type": "function",
								"function": map[string]string{
									"name":      "get_weather",
									"arguments": `{"location":"London"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	resp, err := adapter.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "What's the weather?"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call-1", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Name)
	assert.Equal(t, "London", resp.ToolCalls[0].Params["location"])
}

func TestComplete_SendsToolResultMessages(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]string{"role": "assistant", "content": "Done."},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	_, err := adapter.Complete(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "run tool"},
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "tc-1", Name: "echo", Params: map[string]any{"msg": "hi"}},
				},
			},
			{Role: model.RoleTool, Content: "hi", ToolCallID: "tc-1"},
		},
	})
	require.NoError(t, err)

	// Verify that the tool-result message was sent with the correct role and
	// tool_call_id, and that the assistant tool-call message was serialised in
	// the OpenAI format.
	msgs, _ := capturedBody["messages"].([]interface{})
	require.Len(t, msgs, 3)

	toolResultMsg, _ := msgs[2].(map[string]interface{})
	assert.Equal(t, "tool", toolResultMsg["role"])
	assert.Equal(t, "tc-1", toolResultMsg["tool_call_id"])
}

func TestComplete_SendsRequestedModelName(t *testing.T) {
	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		capturedModel, _ = body["model"].(string)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]string{"role": "assistant", "content": "hi"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	_, err := adapter.Complete(context.Background(), model.Request{
		Model:    "llama3",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "llama3", capturedModel)
}

func TestComplete_DefaultsToLocalWhenModelEmpty(t *testing.T) {
	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		capturedModel, _ = body["model"].(string)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]string{"role": "assistant", "content": "hi"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	adapter := llama.New(srv.URL)
	_, err := adapter.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "local", capturedModel)
}
