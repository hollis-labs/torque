package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

func TestDirIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "torque")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	id, err := config.RecordDirIdentity(dir)
	require.NoError(t, err)
	require.NoError(t, id.Check())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profiles.yaml"), nil, 0o600))
	assert.NoError(t, id.Check(), "writing inside the directory keeps it the same directory")

	require.NoError(t, os.Rename(dir, dir+".moved"))
	assert.Error(t, id.Check(), "a moved directory is gone from the path")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	assert.ErrorContains(t, id.Check(), "no longer the directory recorded at startup", "a recreated directory is a different one")
}
