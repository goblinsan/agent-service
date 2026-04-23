package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/goblinsan/agent-service/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileTool_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	ft := tools.NewFileTool(dir)
	ctx := context.Background()

	_, err := ft.Execute(ctx, map[string]any{
		"op":      "write_file",
		"path":    "hello.txt",
		"content": "world",
	})
	require.NoError(t, err)

	out, err := ft.Execute(ctx, map[string]any{
		"op":   "read_file",
		"path": "hello.txt",
	})
	require.NoError(t, err)
	assert.Equal(t, "world", out)
}

func TestFileTool_ListDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))

	ft := tools.NewFileTool(dir)
	out, err := ft.Execute(context.Background(), map[string]any{
		"op":   "list_dir",
		"path": ".",
	})
	require.NoError(t, err)
	names, ok := out.([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt"}, names)
}

func TestFileTool_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	ft := tools.NewFileTool(dir)

	_, err := ft.Execute(context.Background(), map[string]any{
		"op":   "read_file",
		"path": "../../etc/passwd",
	})
	require.Error(t, err)
}

func TestFileTool_UnknownOp(t *testing.T) {
	ft := tools.NewFileTool(t.TempDir())
	_, err := ft.Execute(context.Background(), map[string]any{
		"op":   "delete_file",
		"path": "x",
	})
	require.Error(t, err)
}

func TestFileTool_Definition(t *testing.T) {
	ft := tools.NewFileTool(".")
	def := ft.Definition()
	assert.Equal(t, "file", def.Name)
	assert.NotEmpty(t, def.Params)
}
