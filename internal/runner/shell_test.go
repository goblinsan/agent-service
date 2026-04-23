package runner_test

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellRunner_EchoAllowed(t *testing.T) {
	sr := runner.NewShellRunner("")
	out, err := sr.Execute(context.Background(), "shell", map[string]any{"cmd": "echo hello"})
	require.NoError(t, err)
	assert.Contains(t, out, "hello")
}

func TestShellRunner_DisallowedCommand(t *testing.T) {
	sr := runner.NewShellRunner("")
	_, err := sr.Execute(context.Background(), "shell", map[string]any{"cmd": "rm -rf /"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestShellRunner_MetacharacterRejected(t *testing.T) {
	sr := runner.NewShellRunner("")
	_, err := sr.Execute(context.Background(), "shell", map[string]any{"cmd": "echo hello | cat"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestShellRunner_EmptyCmd(t *testing.T) {
	sr := runner.NewShellRunner("")
	_, err := sr.Execute(context.Background(), "shell", map[string]any{})
	require.Error(t, err)
}

func TestShellRunner_AllowedCommands(t *testing.T) {
	cmds := runner.AllowedCommands()
	assert.NotEmpty(t, cmds)
	assert.Contains(t, cmds, "echo")
}
