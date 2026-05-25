package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchExecuteHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "test-key" {
			t.Fatalf("missing or wrong subscription token header: %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "stanley cup winner 2026" {
			t.Fatalf("unexpected query: %q", got)
		}
		if got := r.URL.Query().Get("count"); got != "3" {
			t.Fatalf("unexpected count: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{"title": "Cup winner", "url": "https://example.com/a", "description": "Team X won."},
					{"title": "Recap", "url": "https://example.com/b", "description": "Game 7 recap."},
				},
			},
		})
	}))
	defer srv.Close()

	tool := &WebSearchTool{APIKey: "test-key", Endpoint: srv.URL, Client: srv.Client()}

	out, err := tool.Execute(context.Background(), map[string]any{
		"query": "stanley cup winner 2026",
		"count": 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", out)
	}
	if m["count"].(int) != 2 {
		t.Fatalf("expected 2 results returned, got %v", m["count"])
	}
	results := m["results"].([]map[string]string)
	if results[0]["title"] != "Cup winner" || results[0]["url"] != "https://example.com/a" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
}

func TestWebSearchRejectsMissingAPIKey(t *testing.T) {
	tool := &WebSearchTool{}
	_, err := tool.Execute(context.Background(), map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "BRAVE_SEARCH_API_KEY") {
		t.Fatalf("expected api key error, got %v", err)
	}
}

func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	tool := &WebSearchTool{APIKey: "k"}
	_, err := tool.Execute(context.Background(), map[string]any{"query": ""})
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("expected query error, got %v", err)
	}
}

func TestWebSearchPropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer srv.Close()

	tool := &WebSearchTool{APIKey: "k", Endpoint: srv.URL, Client: srv.Client()}
	_, err := tool.Execute(context.Background(), map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestWebSearchClampsCount(t *testing.T) {
	var seenCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCount = r.URL.Query().Get("count")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()
	tool := &WebSearchTool{APIKey: "k", Endpoint: srv.URL, Client: srv.Client()}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "x", "count": 99}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenCount != "10" {
		t.Fatalf("expected count clamped to 10, got %q", seenCount)
	}
}
