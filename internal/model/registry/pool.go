package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/goblinsan/agent-service/internal/model"
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
	node := p.registry.Pick(req.Model)
	if node == nil {
		return nil, fmt.Errorf("registry pool: no healthy node available for model %q", req.Model)
	}
	prov := p.provider(node.URL)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		p.registry.MarkFailed(node.URL)
		return nil, err
	}
	return resp, nil
}

// Stream implements model.Provider.
func (p *Pool) Stream(ctx context.Context, req model.Request, onChunk func(string) error) error {
	node := p.registry.Pick(req.Model)
	if node == nil {
		return fmt.Errorf("registry pool: no healthy node available for model %q", req.Model)
	}
	prov := p.provider(node.URL)
	err := prov.Stream(ctx, req, onChunk)
	if err != nil {
		p.registry.MarkFailed(node.URL)
	}
	return err
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
