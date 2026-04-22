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
