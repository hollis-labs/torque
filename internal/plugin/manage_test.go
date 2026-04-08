package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePluginDir creates a plugin directory with an active plugin.yaml.
func makePluginDir(t *testing.T, pluginsDir, name string) string {
	t.Helper()
	dir := filepath.Join(pluginsDir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, manifestFile),
		[]byte("name: "+name+"\nversion: 1.0.0\n"),
		0o644,
	))
	return dir
}

func TestDisablePlugin(t *testing.T) {
	pluginsDir := t.TempDir()
	makePluginDir(t, pluginsDir, "my-plugin")

	// Disable should succeed.
	err := DisablePlugin(pluginsDir, "my-plugin")
	require.NoError(t, err)

	// The active manifest should be gone.
	_, err = os.Stat(filepath.Join(pluginsDir, "my-plugin", manifestFile))
	assert.True(t, os.IsNotExist(err))

	// The disabled manifest should exist.
	_, err = os.Stat(filepath.Join(pluginsDir, "my-plugin", manifestFileDisabled))
	assert.NoError(t, err)

	// Disabling again should error.
	err = DisablePlugin(pluginsDir, "my-plugin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already disabled")
}

func TestDisablePlugin_NotInstalled(t *testing.T) {
	pluginsDir := t.TempDir()
	err := DisablePlugin(pluginsDir, "nonexistent-plugin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

func TestEnablePlugin(t *testing.T) {
	pluginsDir := t.TempDir()
	makePluginDir(t, pluginsDir, "tog-plugin")

	// Disable first.
	require.NoError(t, DisablePlugin(pluginsDir, "tog-plugin"))
	assert.True(t, IsDisabled(pluginsDir, "tog-plugin"))

	// Enable should succeed.
	err := EnablePlugin(pluginsDir, "tog-plugin")
	require.NoError(t, err)
	assert.False(t, IsDisabled(pluginsDir, "tog-plugin"))

	// The active manifest should be back.
	_, err = os.Stat(filepath.Join(pluginsDir, "tog-plugin", manifestFile))
	assert.NoError(t, err)

	// Enabling again (already enabled) should error.
	err = EnablePlugin(pluginsDir, "tog-plugin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already enabled")
}

func TestEnablePlugin_NoDisabledManifest(t *testing.T) {
	pluginsDir := t.TempDir()
	err := EnablePlugin(pluginsDir, "ghost-plugin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no disabled manifest")
}

func TestPluginInstalledStatus(t *testing.T) {
	pluginsDir := t.TempDir()

	// Not installed.
	assert.Equal(t, "not-installed", GetPluginStatus(pluginsDir, "absent"))

	// Install (create active manifest).
	makePluginDir(t, pluginsDir, "status-plugin")
	assert.Equal(t, "installed", GetPluginStatus(pluginsDir, "status-plugin"))

	// Disable.
	require.NoError(t, DisablePlugin(pluginsDir, "status-plugin"))
	assert.Equal(t, "disabled", GetPluginStatus(pluginsDir, "status-plugin"))
}
