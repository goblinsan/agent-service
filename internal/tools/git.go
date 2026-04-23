package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitTool exposes read-only git operations: git_status, git_log, git_diff.
type GitTool struct {
	repoDir string
}

// NewGitTool returns a GitTool that operates on repoDir.
// If repoDir is empty, the current directory is used.
func NewGitTool(repoDir string) *GitTool {
	if repoDir == "" {
		repoDir = "."
	}
	return &GitTool{repoDir: repoDir}
}

func (g *GitTool) Definition() Tool {
	return Tool{
		Name:        "git",
		Description: "Run read-only git commands inside the repository.",
		Params: []Param{
			{Name: "op", Type: "string", Description: "Operation: git_status | git_log | git_diff", Required: true},
			{Name: "args", Type: "string", Description: "Extra arguments passed to git (optional)", Required: false},
		},
	}
}

func (g *GitTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	op, _ := params["op"].(string)
	extra, _ := params["args"].(string)

	var gitArgs []string
	switch op {
	case "git_status":
		gitArgs = []string{"status", "--short"}
	case "git_log":
		gitArgs = []string{"log", "--oneline", "-20"}
	case "git_diff":
		gitArgs = []string{"diff", "--stat"}
	default:
		return nil, fmt.Errorf("unknown git operation %q", op)
	}

	if extra != "" {
		gitArgs = append(gitArgs, strings.Fields(extra)...)
	}

	return g.run(ctx, gitArgs)
}

func (g *GitTool) run(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoDir
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w – %s", args[0], err, errBuf.String())
	}
	return out.String(), nil
}
