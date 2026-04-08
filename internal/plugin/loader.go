package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	goplugin "github.com/hollis-labs/plugin"
)

// DiscoveredPlugin is a plugin found on disk that has a registered constructor.
type DiscoveredPlugin struct {
	Manifest    *PluginManifest
	Dir         string
	Constructor PluginConstructor
}

// DiscoverPlugins scans pluginsDir for subdirectories containing a plugin.yaml.
// Each discovered plugin must also have a registered constructor; otherwise it is skipped.
// Returns (nil, nil) if pluginsDir does not exist.
func DiscoverPlugins(pluginsDir string) ([]DiscoveredPlugin, error) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read plugins dir %s: %w", pluginsDir, err)
	}

	var discovered []DiscoveredPlugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(pluginsDir, entry.Name())
		manifestPath := filepath.Join(dir, "plugin.yaml")

		manifest, err := ParseManifest(manifestPath)
		if err != nil {
			// No manifest — skip.
			continue
		}

		ctor, ok := LookupConstructor(manifest.Name)
		if !ok {
			// No constructor registered — skip.
			continue
		}

		discovered = append(discovered, DiscoveredPlugin{
			Manifest:    manifest,
			Dir:         dir,
			Constructor: ctor,
		})
	}
	return discovered, nil
}

// LoadDiscovered sorts discovered plugins by their dependency graph and loads them into host.
// Returns the successfully loaded plugins and any per-plugin errors.
func LoadDiscovered(host *ClockworkHost, discovered []DiscoveredPlugin) ([]goplugin.Plugin, []error) {
	sorted, err := sortByDeps(discovered)
	if err != nil {
		return nil, []error{err}
	}

	var loaded []goplugin.Plugin
	var errs []error
	for _, dp := range sorted {
		p := dp.Constructor()
		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load plugin %s: %w", dp.Manifest.Name, err))
			continue
		}
		loaded = append(loaded, p)
	}
	return loaded, errs
}

// LoadRegisteredBuiltins loads all constructors from the global registry that are not yet
// loaded into host. Returns successfully loaded plugins and any per-plugin errors.
func LoadRegisteredBuiltins(host *ClockworkHost) ([]goplugin.Plugin, []error) {
	all := GetRegistered()

	var loaded []goplugin.Plugin
	var errs []error
	for id, ctor := range all {
		if _, ok := host.GetPlugin(id); ok {
			// Already loaded.
			continue
		}
		p := ctor()
		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load builtin %s: %w", id, err))
			continue
		}
		loaded = append(loaded, p)
	}
	return loaded, errs
}

// sortByDeps performs a topological sort of discovered plugins using Kahn's algorithm.
// Dependencies on plugins that are not in the discovered set are treated as external and ignored.
// Returns an error if a cycle is detected.
func sortByDeps(plugins []DiscoveredPlugin) ([]DiscoveredPlugin, error) {
	// Build a name → index map for the discovered set.
	index := make(map[string]int, len(plugins))
	for i, dp := range plugins {
		index[dp.Manifest.Name] = i
	}

	// inDegree[i] = number of internal dependencies not yet satisfied.
	inDegree := make([]int, len(plugins))
	// adj[i] = list of indices that depend on plugins[i].
	adj := make([][]int, len(plugins))

	for i, dp := range plugins {
		for _, dep := range dp.Manifest.Dependencies {
			j, ok := index[dep]
			if !ok {
				// External dependency — skip.
				continue
			}
			inDegree[i]++
			adj[j] = append(adj[j], i)
		}
	}

	// Queue starts with all nodes that have no internal dependencies.
	queue := make([]int, 0, len(plugins))
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	result := make([]DiscoveredPlugin, 0, len(plugins))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, plugins[cur])
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(result) != len(plugins) {
		return nil, fmt.Errorf("plugin dependency cycle detected")
	}
	return result, nil
}
