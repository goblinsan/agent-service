package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// braveSearchEndpoint is the Brave Search API v1 web search endpoint.
// Documented at https://api.search.brave.com/app/documentation/web-search/get-started
const braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

// WebSearchTool performs a Brave web search and returns the top results so the
// LLM can answer questions about current events, recent scores, releases, etc.
type WebSearchTool struct {
	APIKey   string
	Endpoint string // optional override; defaults to braveSearchEndpoint
	Client   *http.Client
}

func (w *WebSearchTool) Definition() Tool {
	return Tool{
		Name: "web_search",
		Description: "Search the public web (Brave Search) and return the top results " +
			"with title, url, and snippet. Use this for anything time-sensitive or beyond " +
			"your training data, such as current events, recent sports results, prices, " +
			"or release notes. Returns a JSON object {results:[{title,url,snippet}], query, count}.",
		Params: []Param{
			{Name: "query", Type: "string", Description: "Natural-language search query", Required: true},
			{Name: "count", Type: "int", Description: "Number of results to return (1-10, default 5)", Required: false},
		},
	}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func (w *WebSearchTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if w.APIKey == "" {
		return nil, fmt.Errorf("web_search: BRAVE_SEARCH_API_KEY not configured")
	}
	query, _ := params["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("web_search: query must not be empty")
	}

	count := 5
	switch v := params["count"].(type) {
	case float64:
		count = int(v)
	case int:
		count = v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			count = n
		}
	}
	if count < 1 {
		count = 1
	}
	if count > 10 {
		count = 10
	}

	endpoint := w.Endpoint
	if endpoint == "" {
		endpoint = braveSearchEndpoint
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("web_search: parse endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(count))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("web_search: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", w.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("web_search: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("web_search: brave returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed braveSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("web_search: decode response: %w", err)
	}

	results := make([]map[string]string, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Description,
		})
	}

	return map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}, nil
}
