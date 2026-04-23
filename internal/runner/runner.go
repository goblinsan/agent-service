package runner

import "context"

// Runner executes a named tool with the supplied parameters and returns the result.
type Runner interface {
	Execute(ctx context.Context, tool string, params map[string]any) (any, error)
}
