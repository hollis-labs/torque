package waitpoll_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

func TestFileExists_True(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	p := waitpoll.NewFileExists()
	assert.Equal(t, "file_exists", p.Type())
	ok, err := p.Evaluate(context.Background(), map[string]any{"path": path})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFileExists_False(t *testing.T) {
	p := waitpoll.NewFileExists()
	ok, err := p.Evaluate(context.Background(), map[string]any{"path": "/definitely/nonexistent/path"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestFileExists_DirectoryAlsoTrue(t *testing.T) {
	dir := t.TempDir()
	p := waitpoll.NewFileExists()
	ok, err := p.Evaluate(context.Background(), map[string]any{"path": dir})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFileExists_Validate(t *testing.T) {
	p := waitpoll.NewFileExists()
	require.Error(t, p.Validate(map[string]any{}))
	require.Error(t, p.Validate(map[string]any{"path": ""}))
	require.NoError(t, p.Validate(map[string]any{"path": "/tmp/x"}))
}
