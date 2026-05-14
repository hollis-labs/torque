package plugin

import (
	"os"
	"path/filepath"
	"testing"

	goplugin "github.com/hollis-labs/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writePluginDir creates a plugin subdirectory with a minimal plugin.yaml.
func writePluginDir(t *testing.T, pluginsDir, name string, deps []string) string {
	t.Helper()
	dir := filepath.Join(pluginsDir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	depYAML := ""
	for _, d := range deps {
		depYAML += "  - " + d + "\n"
	}
	if depYAML != "" {
		depYAML = "dependencies:\n" + depYAML
	}

	content := "name: " + name + "\nversion: 1.0.0\n" + depYAML
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(content), 0o644))
	return dir
}

func TestDiscoverPlugins(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	pluginsDir := t.TempDir()
	writePluginDir(t, pluginsDir, "plugin-x", nil)
	writePluginDir(t, pluginsDir, "plugin-y", nil)

	RegisterPluginConstructor("plugin-x", func() goplugin.Plugin {
		return &testPlugin{id: "plugin-x", name: "plugin-x"}
	})
	RegisterPluginConstructor("plugin-y", func() goplugin.Plugin {
		return &testPlugin{id: "plugin-y", name: "plugin-y"}
	})

	discovered, err := DiscoverPlugins(pluginsDir)
	require.NoError(t, err)
	assert.Len(t, discovered, 2)
}

func TestDiscoverPluginsEmptyDir(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	pluginsDir := t.TempDir()
	discovered, err := DiscoverPlugins(pluginsDir)
	require.NoError(t, err)
	assert.Empty(t, discovered)
}

func TestDiscoverPluginsMissingDir(t *testing.T) {
	discovered, err := DiscoverPlugins("/nonexistent/plugins/dir")
	require.NoError(t, err)
	assert.Nil(t, discovered)
}

func TestDiscoverPluginsSkipsNonDirs(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	pluginsDir := t.TempDir()
	// Write a regular file (not a directory) at the top level.
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "somefile.txt"), []byte("data"), 0o644))
	// Write a valid plugin directory.
	writePluginDir(t, pluginsDir, "plugin-z", nil)
	RegisterPluginConstructor("plugin-z", func() goplugin.Plugin {
		return &testPlugin{id: "plugin-z", name: "plugin-z"}
	})

	discovered, err := DiscoverPlugins(pluginsDir)
	require.NoError(t, err)
	// Only the real plugin dir should be discovered.
	assert.Len(t, discovered, 1)
	assert.Equal(t, "plugin-z", discovered[0].Manifest.Name)
}

func TestLoadDiscovered(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	pluginsDir := t.TempDir()
	writePluginDir(t, pluginsDir, "plug-load-a", nil)
	writePluginDir(t, pluginsDir, "plug-load-b", nil)

	RegisterPluginConstructor("plug-load-a", func() goplugin.Plugin {
		return &testPlugin{id: "plug-load-a", name: "plug-load-a"}
	})
	RegisterPluginConstructor("plug-load-b", func() goplugin.Plugin {
		return &testPlugin{id: "plug-load-b", name: "plug-load-b"}
	})

	discovered, err := DiscoverPlugins(pluginsDir)
	require.NoError(t, err)
	require.Len(t, discovered, 2)

	host := NewTorqueHost(nil, nil)
	loaded, errs := LoadDiscovered(host, discovered)
	assert.Empty(t, errs)
	assert.Len(t, loaded, 2)

	_, ok := host.GetPlugin("plug-load-a")
	assert.True(t, ok)
	_, ok = host.GetPlugin("plug-load-b")
	assert.True(t, ok)
}

func TestLoadDiscoveredDependencyOrder(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	pluginsDir := t.TempDir()
	// plug-dep-b depends on plug-dep-a — a must load first.
	writePluginDir(t, pluginsDir, "plug-dep-a", nil)
	writePluginDir(t, pluginsDir, "plug-dep-b", []string{"plug-dep-a"})

	loadOrder := []string{}

	RegisterPluginConstructor("plug-dep-a", func() goplugin.Plugin {
		return &testPlugin{id: "plug-dep-a", name: "plug-dep-a"}
	})
	RegisterPluginConstructor("plug-dep-b", func() goplugin.Plugin {
		return &testPlugin{id: "plug-dep-b", name: "plug-dep-b", deps: []string{"plug-dep-a"}}
	})

	discovered, err := DiscoverPlugins(pluginsDir)
	require.NoError(t, err)
	require.Len(t, discovered, 2)

	// Sort by deps and verify order.
	sorted, err := sortByDeps(discovered)
	require.NoError(t, err)
	for _, dp := range sorted {
		loadOrder = append(loadOrder, dp.Manifest.Name)
	}

	require.Len(t, loadOrder, 2)
	assert.Equal(t, "plug-dep-a", loadOrder[0])
	assert.Equal(t, "plug-dep-b", loadOrder[1])
}
