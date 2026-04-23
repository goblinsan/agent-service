package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPRunner calls an external HTTP endpoint to execute a tool.
// The endpoint receives a JSON POST body of the form:
//
//	{"tool": "<name>", "params": { ... }}
//
// and is expected to respond with a JSON body that is returned as the result.
type HTTPRunner struct {
	baseURL string
	client  *http.Client
}

// NewHTTPRunner returns an HTTPRunner that posts tool calls to baseURL.
// If client is nil a default client with a 15-second timeout is used.
func NewHTTPRunner(baseURL string, client *http.Client) *HTTPRunner {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPRunner{baseURL: baseURL, client: client}
}

// Execute serialises the call as JSON and POSTs it to the runner endpoint.
func (h *HTTPRunner) Execute(ctx context.Context, tool string, params map[string]any) (any, error) {
	payload := map[string]any{
		"tool":   tool,
		"params": params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("http runner marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http runner create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http runner: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("http runner: server returned %d: %s", resp.StatusCode, b)
	}

	var result any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("http runner decode response: %w", err)
	}
	return result, nil
}
