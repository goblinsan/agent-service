package tools_test

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactTool_SaveAndLoad(t *testing.T) {
	at := tools.NewArtifactTool()
	ctx := context.Background()

	_, err := at.Execute(ctx, map[string]any{
		"op":      "save_artifact",
		"name":    "report",
		"content": "summary text",
	})
	require.NoError(t, err)

	out, err := at.Execute(ctx, map[string]any{
		"op":   "load_artifact",
		"name": "report",
	})
	require.NoError(t, err)
	assert.Equal(t, "summary text", out)
}

func TestArtifactTool_ListArtifacts(t *testing.T) {
	at := tools.NewArtifactTool()
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		_, err := at.Execute(ctx, map[string]any{"op": "save_artifact", "name": name, "content": name})
		require.NoError(t, err)
	}

	out, err := at.Execute(ctx, map[string]any{"op": "list_artifacts"})
	require.NoError(t, err)
	names, ok := out.([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, names)
}

func TestArtifactTool_LoadNotFound(t *testing.T) {
	at := tools.NewArtifactTool()
	_, err := at.Execute(context.Background(), map[string]any{
		"op":   "load_artifact",
		"name": "ghost",
	})
	require.Error(t, err)
}

func TestArtifactTool_SaveMissingName(t *testing.T) {
	at := tools.NewArtifactTool()
	_, err := at.Execute(context.Background(), map[string]any{
		"op":      "save_artifact",
		"content": "data",
	})
	require.Error(t, err)
}

func TestArtifactTool_UnknownOp(t *testing.T) {
	at := tools.NewArtifactTool()
	_, err := at.Execute(context.Background(), map[string]any{"op": "delete_artifact"})
	require.Error(t, err)
}

func TestArtifactTool_Definition(t *testing.T) {
	at := tools.NewArtifactTool()
	def := at.Definition()
	assert.Equal(t, "artifact", def.Name)
}
