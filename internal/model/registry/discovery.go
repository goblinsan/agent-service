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
	Status      string `json:"status"`
	LoadedModel string `json:"loaded_model"`
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.URL, "/")+"/api/node", nil)
	if err != nil {
		return updated, false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return updated, false, fmt.Errorf("probe %s: %w", cfg.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return updated, false, fmt.Errorf("probe %s: unexpected status %d", cfg.URL, resp.StatusCode)
	}

	var payload nodeCapabilityResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return updated, false, fmt.Errorf("probe %s: decode /api/node: %w", cfg.URL, err)
	}

	loadedModel := strings.TrimSpace(payload.LoadedModel)
	healthy := payload.Status == "ok" && loadedModel != ""
	if loadedModel != "" {
		updated.Models = []string{loadedModel}
	}
	return updated, healthy, nil
}
