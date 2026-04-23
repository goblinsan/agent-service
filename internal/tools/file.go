package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// FileTool provides read_file, write_file and list_dir operations.
// rootDir constrains all operations to a single directory tree.
type FileTool struct {
	rootDir string
}

// NewFileTool returns a FileTool scoped to rootDir.
// If rootDir is empty, it defaults to the current working directory.
func NewFileTool(rootDir string) *FileTool {
	if rootDir == "" {
		rootDir = "."
	}
	return &FileTool{rootDir: rootDir}
}

func (f *FileTool) Definition() Tool {
	return Tool{
		Name:        "file",
		Description: "Read, write, or list files within the workspace.",
		Params: []Param{
			{Name: "op", Type: "string", Description: "Operation: read_file | write_file | list_dir", Required: true},
			{Name: "path", Type: "string", Description: "Relative path to the file or directory", Required: true},
			{Name: "content", Type: "string", Description: "Content to write (required for write_file)", Required: false},
		},
	}
}

func (f *FileTool) Execute(_ context.Context, params map[string]any) (any, error) {
	op, _ := params["op"].(string)
	rel, _ := params["path"].(string)

	abs, err := f.safePath(rel)
	if err != nil {
		return nil, err
	}

	switch op {
	case "read_file":
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("read_file: %w", err)
		}
		return string(data), nil

	case "write_file":
		content, _ := params["content"].(string)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, fmt.Errorf("write_file mkdir: %w", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write_file: %w", err)
		}
		return "ok", nil

	case "list_dir":
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, fmt.Errorf("list_dir: %w", err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names, nil

	default:
		return nil, fmt.Errorf("unknown file operation %q", op)
	}
}

// safePath resolves rel relative to rootDir and verifies it stays inside rootDir.
func (f *FileTool) safePath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	abs := filepath.Join(f.rootDir, filepath.Clean("/"+rel))
	root, err := filepath.Abs(f.rootDir)
	if err != nil {
		return "", err
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	// Ensure the resolved path is inside rootDir.
	rel2, err := filepath.Rel(root, abs)
	if err != nil || len(rel2) >= 2 && rel2[:2] == ".." {
		return "", fmt.Errorf("path %q escapes workspace", rel)
	}
	return abs, nil
}
