package tools

import (
	"context"
	"fmt"
	"sync"
)

// ArtifactTool provides in-memory key/value storage for agent artifacts.
// It supports save_artifact, load_artifact, and list_artifacts operations.
type ArtifactTool struct {
	mu    sync.RWMutex
	store map[string]string
}

// NewArtifactTool returns an empty ArtifactTool.
func NewArtifactTool() *ArtifactTool {
	return &ArtifactTool{store: make(map[string]string)}
}

func (a *ArtifactTool) Definition() Tool {
	return Tool{
		Name:        "artifact",
		Description: "Save and retrieve named text artifacts across agent steps.",
		Params: []Param{
			{Name: "op", Type: "string", Description: "Operation: save_artifact | load_artifact | list_artifacts", Required: true},
			{Name: "name", Type: "string", Description: "Artifact name (required for save/load)", Required: false},
			{Name: "content", Type: "string", Description: "Content to save (required for save_artifact)", Required: false},
		},
	}
}

func (a *ArtifactTool) Execute(_ context.Context, params map[string]any) (any, error) {
	op, _ := params["op"].(string)

	switch op {
	case "save_artifact":
		name, _ := params["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("save_artifact: name must not be empty")
		}
		content, _ := params["content"].(string)
		a.mu.Lock()
		a.store[name] = content
		a.mu.Unlock()
		return "ok", nil

	case "load_artifact":
		name, _ := params["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("load_artifact: name must not be empty")
		}
		a.mu.RLock()
		v, ok := a.store[name]
		a.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("artifact %q not found", name)
		}
		return v, nil

	case "list_artifacts":
		a.mu.RLock()
		names := make([]string, 0, len(a.store))
		for k := range a.store {
			names = append(names, k)
		}
		a.mu.RUnlock()
		return names, nil

	default:
		return nil, fmt.Errorf("unknown artifact operation %q", op)
	}
}
