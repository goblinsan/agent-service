package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPTool performs outbound HTTP GET requests.
type HTTPTool struct {
	client *http.Client
}

// NewHTTPTool returns an HTTPTool with the provided client.
// If client is nil, a default client with a 15-second timeout is used.
func NewHTTPTool(client *http.Client) *HTTPTool {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPTool{client: client}
}

func (h *HTTPTool) Definition() Tool {
	return Tool{
		Name:        "http",
		Description: "Perform an HTTP GET request and return the response body.",
		Params: []Param{
			{Name: "url", Type: "string", Description: "Target URL", Required: true},
		},
	}
}

func (h *HTTPTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	url, _ := params["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("url must not be empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http_get create request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("http_get read body: %w", err)
	}

	return map[string]any{
		"status": resp.StatusCode,
		"body":   string(body),
	}, nil
}
