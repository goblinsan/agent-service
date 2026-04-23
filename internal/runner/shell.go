package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// allowedShellCommands is the set of executables the ShellRunner may invoke.
// Only read-only / informational commands are permitted to keep execution safe.
var allowedShellCommands = map[string]bool{
	"echo": true,
	"cat":  true,
	"ls":   true,
	"pwd":  true,
	"grep": true,
	"wc":   true,
	"sort": true,
	"head": true,
	"tail": true,
	"date": true,
	"env":  true,
}

// ShellRunner runs a constrained subset of shell commands.
// It does NOT use a shell interpreter; each command is executed directly via
// exec.Command to prevent injection through shell metacharacters.
type ShellRunner struct {
	workDir string
}

// NewShellRunner returns a ShellRunner whose commands run in workDir.
// If workDir is empty the current directory is used.
func NewShellRunner(workDir string) *ShellRunner {
	return &ShellRunner{workDir: workDir}
}

// Execute accepts the tool name "shell" and expects:
//
//	params["cmd"] string – the command line to execute (e.g. "ls -la /tmp")
//
// Only the executables listed in allowedShellCommands may be used.
// Shell metacharacters (|, ;, &, >, <, $, `, \n) in the command string are
// rejected to guard against injection.
func (s *ShellRunner) Execute(ctx context.Context, _ string, params map[string]any) (any, error) {
	cmdLine, _ := params["cmd"].(string)
	if cmdLine == "" {
		return nil, fmt.Errorf("shell: cmd parameter is required")
	}

	if err := rejectMetacharacters(cmdLine); err != nil {
		return nil, err
	}

	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		return nil, fmt.Errorf("shell: empty command")
	}

	executable := parts[0]
	if !allowedShellCommands[executable] {
		return nil, fmt.Errorf("shell: command %q is not allowed", executable)
	}

	cmd := exec.CommandContext(ctx, executable, parts[1:]...)
	cmd.Dir = s.workDir
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("shell: %s failed: %w – %s", executable, err, errBuf.String())
	}
	return out.String(), nil
}

// rejectMetacharacters returns an error if cmdLine contains shell metacharacters
// that could be used to escape the constrained execution context.
func rejectMetacharacters(cmdLine string) error {
	const forbidden = "|;&><`$\n\r"
	for _, ch := range forbidden {
		if strings.ContainsRune(cmdLine, ch) {
			return fmt.Errorf("shell: command contains forbidden character %q", ch)
		}
	}
	return nil
}

// AllowedCommands returns a copy of the allowlist so callers can inspect it.
func AllowedCommands() []string {
	cmds := make([]string, 0, len(allowedShellCommands))
	for k := range allowedShellCommands {
		cmds = append(cmds, k)
	}
	return cmds
}
