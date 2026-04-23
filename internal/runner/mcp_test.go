package runner_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mcpServer(t *testing.T, result any, rpcErr *struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
		}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resultBytes, _ := json.Marshal(result)
			resp["result"] = json.RawMessage(resultBytes)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestMCPRunner_Execute_Success(t *testing.T) {
	srv := mcpServer(t, map[string]any{"output": "hello from mcp"}, nil)
	defer srv.Close()

	r := runner.NewMCPRunner(srv.URL, nil)
	out, err := r.Execute(context.Background(), "echo", map[string]any{"msg": "hello"})
	require.NoError(t, err)

	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "hello from mcp", m["output"])
}

func TestMCPRunner_Execute_RPCError(t *testing.T) {
	srv := mcpServer(t, nil, &struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: -32601, Message: "method not found"})
	defer srv.Close()

	r := runner.NewMCPRunner(srv.URL, nil)
	_, err := r.Execute(context.Background(), "no_such_tool", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "method not found")
}

func TestMCPRunner_Execute_ServerUnavailable(t *testing.T) {
	// Point at a port that has nothing listening.
	r := runner.NewMCPRunner("http://127.0.0.1:1", nil)
	_, err := r.Execute(context.Background(), "echo", map[string]any{})
	require.Error(t, err)
}

func TestMCPRunner_Execute_SendsCorrectMethod(t *testing.T) {
	var receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedMethod, _ = req["method"].(string)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  "ok",
		})
	}))
	defer srv.Close()

	r := runner.NewMCPRunner(srv.URL, nil)
	_, err := r.Execute(context.Background(), "my_tool", map[string]any{"key": "val"})
	require.NoError(t, err)
	assert.Equal(t, "tools/call", receivedMethod)
}
