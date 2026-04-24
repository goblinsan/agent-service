// Package registry implements a multi-node llm-service backend registry.
//
// A Registry holds the configuration and transient health state for one or
// more llm-service nodes.  Callers use Pick to obtain the URL of a healthy
// node that supports the requested model, and MarkFailed / MarkHealthy to
// inform the registry of observed backend errors and recoveries.
package registry

import (
	"sync"
	"time"
)

// NodeConfig describes a single llm-service backend node.
type NodeConfig struct {
	// Name is a human-readable label for this node (e.g. "node-1").
	Name string
	// URL is the base URL of the llm-service HTTP API (e.g. "http://host:8080").
	URL string
	// Models is the set of model names served by this node.
	// An empty slice means the node can serve any model.
	Models []string
}

// nodeState couples a NodeConfig with transient health-tracking data.
type nodeState struct {
	config      NodeConfig
	healthy     bool
	failures    int
	lastFailure time.Time
}

// Registry tracks the availability of one or more llm-service nodes.
// It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	nodes []*nodeState
}

// New returns a Registry initialised with the supplied node configurations.
// All nodes are considered healthy at construction time.
func New(configs []NodeConfig) *Registry {
	nodes := make([]*nodeState, len(configs))
	for i, c := range configs {
		nodes[i] = &nodeState{config: c, healthy: true}
	}
	return &Registry{nodes: nodes}
}

// Pick returns the configuration of the first healthy node that supports the
// requested model name.  When model is empty any healthy node qualifies.
// Returns nil when no suitable node is available.
func (r *Registry) Pick(model string) *NodeConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.nodes {
		if !n.healthy {
			continue
		}
		if supportsModel(n.config.Models, model) {
			c := n.config
			return &c
		}
	}
	return nil
}

// MarkFailed records a failure for the node at the given URL and marks it as
// unhealthy so it will be skipped by subsequent Pick calls.
func (r *Registry) MarkFailed(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.nodes {
		if n.config.URL == url {
			n.failures++
			n.lastFailure = time.Now()
			n.healthy = false
			return
		}
	}
}

// MarkHealthy clears the failure state for the node at the given URL and marks
// it healthy so it becomes eligible for Pick again.
func (r *Registry) MarkHealthy(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.nodes {
		if n.config.URL == url {
			n.healthy = true
			n.failures = 0
			return
		}
	}
}

// Nodes returns a snapshot of all registered node configurations, regardless
// of their current health state.
func (r *Registry) Nodes() []NodeConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeConfig, len(r.nodes))
	for i, n := range r.nodes {
		out[i] = n.config
	}
	return out
}

// supportsModel reports whether the models slice contains the requested name.
// When the slice is empty the node is considered to accept any model.
func supportsModel(models []string, requested string) bool {
	if len(models) == 0 {
		return true
	}
	for _, m := range models {
		if m == requested {
			return true
		}
	}
	return false
}
