package runner_test

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/runner"
	"github.com/goblinsan/agent-service/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoHandler returns its "msg" param as the result.
type echoHandler struct{}

func (e *echoHandler) Definition() tools.Tool {
	return tools.Tool{
		Name:        "echo",
		Description: "Returns the msg parameter",
		Params: []tools.Param{
			{Name: "msg", Type: "string", Required: true},
		},
	}
}

func (e *echoHandler) Execute(_ context.Context, params map[string]any) (any, error) {
	return params["msg"], nil
}

func TestNativeRunner_Execute(t *testing.T) {
	reg := tools.NewRegistry()
	require.NoError(t, reg.Register(&echoHandler{}))

	nr := runner.NewNativeRunner(reg)
	out, err := nr.Execute(context.Background(), "echo", map[string]any{"msg": "hello"})
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

func TestNativeRunner_UnknownTool(t *testing.T) {
	reg := tools.NewRegistry()
	nr := runner.NewNativeRunner(reg)
	_, err := nr.Execute(context.Background(), "ghost", map[string]any{})
	require.ErrorIs(t, err, tools.ErrNotFound)
}

func TestNativeRunner_ValidationError(t *testing.T) {
	reg := tools.NewRegistry()
	require.NoError(t, reg.Register(&echoHandler{}))

	nr := runner.NewNativeRunner(reg)
	// "msg" is required but not supplied
	_, err := nr.Execute(context.Background(), "echo", map[string]any{})
	require.Error(t, err)
}
