package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const nodeProbeTimeout = 5 * time.Second

type nodeCapabilityResponse struct {
	Status                string `json:"status"`
	LoadedModel           string `json:"loaded_model"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	MaxTokens             int    `json:"max_tokens"`
	CtxSize               int    `json:"ctx_size"`
}

type nodeMetricsResponse struct {
	ActiveRequests int `json:"active_requests"`
}

// RefreshFromNodes probes every registered llm-service node via /api/node and
// updates its routing metadata in place. Nodes only become eligible for model
// selection when their capability endpoint reports status=ok and a loaded
// model name.
func (r *Registry) RefreshFromNodes(ctx context.Context, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: nodeProbeTimeout}
	}
	nodes := r.Nodes()
	var firstErr error
	for _, node := range nodes {
		updated, healthy, err := probeNode(ctx, client, node)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		r.UpdateNode(updated, healthy)
	}
	return firstErr
}

func probeNode(ctx context.Context, client *http.Client, cfg NodeConfig) (NodeConfig, bool, error) {
	updated := cfg
	updated.Models = nil

	payload, err := fetchJSON[nodeCapabilityResponse](ctx, client, strings.TrimRight(cfg.URL, "/")+"/api/node")
	if err != nil {
		return updated, false, fmt.Errorf("probe %s: %w", cfg.URL, err)
	}

	loadedModel := strings.TrimSpace(payload.LoadedModel)
	healthy := payload.Status == "ok" && loadedModel != ""
	if loadedModel != "" {
		updated.Models = []string{loadedModel}
	}
	updated.MaxConcurrentRequests = payload.MaxConcurrentRequests
	updated.MaxTokens = payload.MaxTokens
	updated.CtxSize = payload.CtxSize

	metricsPayload, metricsErr := fetchJSON[nodeMetricsResponse](ctx, client, strings.TrimRight(cfg.URL, "/")+"/api/metrics")
	if metricsErr == nil {
		updated.ActiveRequests = metricsPayload.ActiveRequests
	} else {
		updated.ActiveRequests = 0
	}

	if metricsErr != nil {
		return updated, healthy, fmt.Errorf("probe %s metrics: %w", cfg.URL, metricsErr)
	}
	return updated, healthy, nil
}

func fetchJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var payload T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return payload, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return payload, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return payload, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return payload, err
	}
	return payload, nil
}
