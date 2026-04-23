// Package router provides a model.Provider implementation that dispatches
// requests to one of several configured back-end providers based on a
// list of routing rules.
//
// Rules are evaluated in order; the first matching rule wins.  If no rule
// matches, the router falls back to the default provider.
package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/goblinsan/agent-service/internal/model"
)

// Rule maps a model-name prefix to a named provider.
// If Prefix is empty the rule matches every request (use as a catch-all).
type Rule struct {
	// Prefix is matched against the Model field of the incoming Request.
	// Matching is case-insensitive prefix matching.
	Prefix   string
	Provider string // key into Router.providers
}

// Router is a model.Provider that dispatches to registered back-ends.
type Router struct {
	providers map[string]model.Provider
	rules     []Rule
	def       model.Provider // default provider used when no rule matches
}

// New returns a Router with the given default provider.
// Additional providers can be added via AddProvider and rules via AddRule.
func New(defaultProvider model.Provider) *Router {
	return &Router{
		providers: make(map[string]model.Provider),
		def:       defaultProvider,
	}
}

// AddProvider registers a named provider.
func (r *Router) AddProvider(name string, p model.Provider) {
	r.providers[name] = p
}

// AddRule appends a routing rule.  Rules are evaluated in the order they are added.
func (r *Router) AddRule(rule Rule) {
	r.rules = append(r.rules, rule)
}

// route returns the provider that should handle req.
func (r *Router) route(req model.Request) model.Provider {
	modelName := req.Model
	for _, rule := range r.rules {
		if rule.Prefix == "" || strings.HasPrefix(strings.ToLower(modelName), strings.ToLower(rule.Prefix)) {
			if p, ok := r.providers[rule.Provider]; ok {
				return p
			}
		}
	}
	if r.def != nil {
		return r.def
	}
	return nil
}

// Complete implements model.Provider.
func (r *Router) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	p := r.route(req)
	if p == nil {
		return nil, fmt.Errorf("router: no provider available for model %q", req.Model)
	}
	return p.Complete(ctx, req)
}

// Stream implements model.Provider.
func (r *Router) Stream(ctx context.Context, req model.Request, onChunk func(string) error) error {
	p := r.route(req)
	if p == nil {
		return fmt.Errorf("router: no provider available for model %q", req.Model)
	}
	return p.Stream(ctx, req, onChunk)
}
