package tools

import "context"

// Param describes a single input parameter accepted by a Tool.
type Param struct {
	Name        string
	Type        string // "string", "int", "bool"
	Description string
	Required    bool
}

// Tool is the static definition (name, description, parameters) of a callable tool.
type Tool struct {
	Name        string
	Description string
	Params      []Param
}

// Handler is implemented by every built-in and user-supplied tool.
type Handler interface {
	// Definition returns the tool's metadata.
	Definition() Tool
	// Execute runs the tool with the given parameters and returns the result.
	Execute(ctx context.Context, params map[string]any) (any, error)
}
