package tools_test

import (
	"testing"

	"github.com/goblinsan/agent-service/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tool(params ...tools.Param) tools.Tool {
	return tools.Tool{Name: "t", Params: params}
}

func TestValidate_RequiredPresent(t *testing.T) {
	tl := tool(tools.Param{Name: "msg", Type: "string", Required: true})
	err := tools.Validate(tl, map[string]any{"msg": "hello"})
	require.NoError(t, err)
}

func TestValidate_RequiredMissing(t *testing.T) {
	tl := tool(tools.Param{Name: "msg", Type: "string", Required: true})
	err := tools.Validate(tl, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "msg")
}

func TestValidate_OptionalMissing(t *testing.T) {
	tl := tool(tools.Param{Name: "msg", Type: "string", Required: false})
	err := tools.Validate(tl, map[string]any{})
	require.NoError(t, err)
}

func TestValidate_StringOK(t *testing.T) {
	tl := tool(tools.Param{Name: "s", Type: "string", Required: true})
	require.NoError(t, tools.Validate(tl, map[string]any{"s": "value"}))
}

func TestValidate_StringWrongType(t *testing.T) {
	tl := tool(tools.Param{Name: "s", Type: "string", Required: true})
	err := tools.Validate(tl, map[string]any{"s": 42})
	require.Error(t, err)
}

func TestValidate_IntFromNumber(t *testing.T) {
	tl := tool(tools.Param{Name: "n", Type: "int", Required: true})
	require.NoError(t, tools.Validate(tl, map[string]any{"n": 7}))
	require.NoError(t, tools.Validate(tl, map[string]any{"n": float64(3.0)}))
}

func TestValidate_IntFromString(t *testing.T) {
	tl := tool(tools.Param{Name: "n", Type: "int", Required: true})
	require.NoError(t, tools.Validate(tl, map[string]any{"n": "42"}))
}

func TestValidate_IntBadString(t *testing.T) {
	tl := tool(tools.Param{Name: "n", Type: "int", Required: true})
	err := tools.Validate(tl, map[string]any{"n": "abc"})
	require.Error(t, err)
}

func TestValidate_BoolOK(t *testing.T) {
	tl := tool(tools.Param{Name: "flag", Type: "bool", Required: true})
	require.NoError(t, tools.Validate(tl, map[string]any{"flag": true}))
	require.NoError(t, tools.Validate(tl, map[string]any{"flag": "true"}))
}

func TestValidate_BoolBadString(t *testing.T) {
	tl := tool(tools.Param{Name: "flag", Type: "bool", Required: true})
	err := tools.Validate(tl, map[string]any{"flag": "maybe"})
	require.Error(t, err)
}
