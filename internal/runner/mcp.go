package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// MCPRunner calls a Model Context Protocol (MCP) server to execute tools.
// It uses the JSON-RPC 2.0 framing expected by the MCP specification:
//
//	POST <endpoint>
//	Content-Type: application/json
//
//	{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"<tool>","arguments":{...}}}
//
// The server response must be a JSON-RPC 2.0 result object.
type MCPRunner struct {
	endpoint string
	client   *http.Client
	nextID   atomic.Int64
}

// NewMCPRunner returns an MCPRunner targeting endpoint.
// If client is nil a default client with a 15-second timeout is used.
func NewMCPRunner(endpoint string, client *http.Client) *MCPRunner {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &MCPRunner{endpoint: endpoint, client: client}
}

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  mcpCallParams  `json:"params"`
}

type mcpCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Execute calls the MCP server using the tools/call method.
func (m *MCPRunner) Execute(ctx context.Context, tool string, params map[string]any) (any, error) {
	id := int(m.nextID.Add(1))

	rpcReq := mcpRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: mcpCallParams{
			Name:      tool,
			Arguments: params,
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("mcp runner marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp runner create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp runner: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("mcp runner read response: %w", err)
	}

	var rpcResp mcpResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp runner decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var result any
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp runner decode result: %w", err)
	}
	return result, nil
}
