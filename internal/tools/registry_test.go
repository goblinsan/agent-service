package tools_test

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubHandler is a minimal Handler used in registry tests.
type stubHandler struct {
	name   string
	result any
}

func (s *stubHandler) Definition() tools.Tool {
	return tools.Tool{
		Name:        s.name,
		Description: "stub",
		Params: []tools.Param{
			{Name: "x", Type: "string", Required: true},
		},
	}
}

func (s *stubHandler) Execute(_ context.Context, _ map[string]any) (any, error) {
	return s.result, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := tools.NewRegistry()
	h := &stubHandler{name: "echo", result: "pong"}

	require.NoError(t, r.Register(h))

	got, err := r.Get("echo")
	require.NoError(t, err)
	assert.Equal(t, "echo", got.Definition().Name)
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := tools.NewRegistry()
	h := &stubHandler{name: "dup"}
	require.NoError(t, r.Register(h))
	err := r.Register(h)
	require.Error(t, err)
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := tools.NewRegistry()
	_, err := r.Get("missing")
	require.ErrorIs(t, err, tools.ErrNotFound)
}

func TestRegistry_List(t *testing.T) {
	r := tools.NewRegistry()
	require.NoError(t, r.Register(&stubHandler{name: "a"}))
	require.NoError(t, r.Register(&stubHandler{name: "b"}))

	list := r.List()
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Name)
	}
	assert.ElementsMatch(t, []string{"a", "b"}, names)
}

func TestRegistry_Execute(t *testing.T) {
	r := tools.NewRegistry()
	require.NoError(t, r.Register(&stubHandler{name: "greet", result: "hello"}))

	out, err := r.Execute(context.Background(), "greet", map[string]any{"x": "world"})
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

func TestRegistry_Execute_ValidationError(t *testing.T) {
	r := tools.NewRegistry()
	require.NoError(t, r.Register(&stubHandler{name: "strict"}))

	// missing required param "x"
	_, err := r.Execute(context.Background(), "strict", map[string]any{})
	require.Error(t, err)
}
