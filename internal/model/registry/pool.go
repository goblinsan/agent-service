package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
)

var (
	transientCapacityRetries = 15
	transientCapacityDelay   = 2 * time.Second
)

// Pool is a model.Provider backed by a Registry.  On each request it picks the
// first healthy node that supports the requested model, delegates the call, and
// marks the node as failed if the call returns an error.
type Pool struct {
	registry  *Registry
	mu        sync.Mutex
	providers map[string]model.Provider
	newNode   func(url string) model.Provider
}

// NewPool returns a Pool that draws nodes from reg.  newNode is called once per
// unique node URL to construct the underlying provider; llama.New is the
// typical value passed here.
func NewPool(reg *Registry, newNode func(url string) model.Provider) *Pool {
	p := &Pool{
		registry:  reg,
		providers: make(map[string]model.Provider),
		newNode:   newNode,
	}
	// Pre-warm providers for every known node.
	for _, cfg := range reg.Nodes() {
		p.providers[cfg.URL] = newNode(cfg.URL)
	}
	return p
}

// Complete implements model.Provider.
func (p *Pool) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	for attempt := 0; ; attempt++ {
		node := p.pickNode(req)
		if node == nil {
			return nil, fmt.Errorf("registry pool: no healthy node available for %s", routeDesc(req))
		}
		prov := p.provider(node.URL)
		resp, err := prov.Complete(ctx, req)
		if err != nil {
			if isTransientCapacityError(err) {
				if attempt < transientCapacityRetries {
					if waitForCapacityRetry(ctx) != nil {
						return nil, err
					}
					continue
				}
			} else {
				p.registry.MarkFailed(node.URL)
			}
			return nil, err
		}
		p.registry.MarkHealthy(node.URL)
		return resp, nil
	}
}

// Stream implements model.Provider.
func (p *Pool) Stream(ctx context.Context, req model.Request, onChunk func(string) error) error {
	_, err := p.StreamComplete(ctx, req, onChunk)
	return err
}

func (p *Pool) StreamComplete(ctx context.Context, req model.Request, onChunk func(string) error) (*model.Response, error) {
	return p.StreamCompleteWithReasoning(ctx, req, onChunk, nil)
}

func (p *Pool) StreamCompleteWithReasoning(ctx context.Context, req model.Request, onChunk func(string) error, onReasoning func(string) error) (*model.Response, error) {
	for attempt := 0; ; attempt++ {
		node := p.pickNode(req)
		if node == nil {
			return nil, fmt.Errorf("registry pool: no healthy node available for %s", routeDesc(req))
		}
		prov := p.provider(node.URL)
		resp, err := streamCompleteWithReasoning(ctx, prov, req, onChunk, onReasoning)
		if err != nil {
			if isTransientCapacityError(err) {
				if attempt < transientCapacityRetries {
					if waitForCapacityRetry(ctx) != nil {
						return nil, err
					}
					continue
				}
			} else {
				p.registry.MarkFailed(node.URL)
			}
			return nil, err
		}
		p.registry.MarkHealthy(node.URL)
		return resp, nil
	}
}

type streamCompleter interface {
	StreamComplete(ctx context.Context, req model.Request, onChunk func(string) error) (*model.Response, error)
}

type reasoningStreamCompleter interface {
	StreamCompleteWithReasoning(ctx context.Context, req model.Request, onChunk func(string) error, onReasoning func(string) error) (*model.Response, error)
}

func streamComplete(ctx context.Context, prov model.Provider, req model.Request, onChunk func(string) error) (*model.Response, error) {
	return streamCompleteWithReasoning(ctx, prov, req, onChunk, nil)
}

func streamCompleteWithReasoning(ctx context.Context, prov model.Provider, req model.Request, onChunk func(string) error, onReasoning func(string) error) (*model.Response, error) {
	if streamingProvider, ok := prov.(reasoningStreamCompleter); ok {
		return streamingProvider.StreamCompleteWithReasoning(ctx, req, onChunk, onReasoning)
	}
	if streamingProvider, ok := prov.(streamCompleter); ok {
		return streamingProvider.StreamComplete(ctx, req, onChunk)
	}
	var content strings.Builder
	err := prov.Stream(ctx, req, func(chunk string) error {
		content.WriteString(chunk)
		if onChunk == nil {
			return nil
		}
		return onChunk(chunk)
	})
	if err != nil {
		return nil, err
	}
	return &model.Response{Content: content.String(), FinishReason: "stop"}, nil
}

// pickNode returns the target node for req.  When req.BackendNode is set the
// pool routes to that named node (no fallback — explicit pinning by the
// caller is honoured strictly so misconfiguration surfaces as a clear error).
// Otherwise it falls back to model-based selection.
func (p *Pool) pickNode(req model.Request) *NodeConfig {
	if req.BackendNode != "" {
		return p.registry.PickByName(req.BackendNode)
	}
	return p.registry.Pick(req.Model, req.EstimatedPromptTokens, req.MaxTokens)
}

func routeDesc(req model.Request) string {
	if req.BackendNode != "" {
		return fmt.Sprintf("node %q", req.BackendNode)
	}
	return fmt.Sprintf("model %q", req.Model)
}

func isTransientCapacityError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status 429") || strings.Contains(message, "slot busy") || strings.Contains(message, "retry later")
}

func waitForCapacityRetry(ctx context.Context) error {
	timer := time.NewTimer(transientCapacityDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// provider returns the cached provider for url, creating it on first access.
func (p *Pool) provider(url string) model.Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	prov, ok := p.providers[url]
	if !ok {
		prov = p.newNode(url)
		p.providers[url] = prov
	}
	return prov
}
