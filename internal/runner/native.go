package runner

import (
	"context"

	"github.com/goblinsan/agent-service/internal/tools"
)

// NativeRunner dispatches tool calls to a tools.Registry.
// It is the default runner used by the agent for built-in tools.
type NativeRunner struct {
	registry *tools.Registry
}

// NewNativeRunner returns a NativeRunner backed by r.
func NewNativeRunner(r *tools.Registry) *NativeRunner {
	return &NativeRunner{registry: r}
}

// Execute validates params and runs the tool registered under tool.
func (n *NativeRunner) Execute(ctx context.Context, tool string, params map[string]any) (any, error) {
	return n.registry.Execute(ctx, tool, params)
}
