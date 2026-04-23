package tools

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrNotFound is returned when a tool name is not registered.
var ErrNotFound = errors.New("tool not found")

// Registry stores named tool handlers and dispatches calls to them.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Handler
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Handler)}
}

// Register adds h to the registry. Returns an error if the name is already taken.
func (r *Registry) Register(h Handler) error {
	name := h.Definition().Name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = h
	return nil
}

// Get returns the Handler for the given tool name.
func (r *Registry) Get(name string) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return h, nil
}

// List returns the definitions of all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, h := range r.tools {
		out = append(out, h.Definition())
	}
	return out
}

// Execute validates params and runs the named tool.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (any, error) {
	h, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	if err := Validate(h.Definition(), params); err != nil {
		return nil, fmt.Errorf("invalid params for %s: %w", name, err)
	}
	return h.Execute(ctx, params)
}
