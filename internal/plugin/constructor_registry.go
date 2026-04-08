package plugin

import (
	"fmt"
	"sync"

	goplugin "github.com/hollis-labs/plugin"
)

// PluginConstructor is a zero-argument factory function that creates a Plugin instance.
type PluginConstructor func() goplugin.Plugin

var (
	registryMu     sync.RWMutex
	pluginRegistry = map[string]PluginConstructor{}
)

// RegisterPluginConstructor registers a constructor for pluginID.
// Panics if the same ID is registered twice.
func RegisterPluginConstructor(id string, constructor PluginConstructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := pluginRegistry[id]; exists {
		panic(fmt.Sprintf("plugin constructor already registered for id %q", id))
	}
	pluginRegistry[id] = constructor
}

// LookupConstructor retrieves the constructor for pluginID.
func LookupConstructor(id string) (PluginConstructor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := pluginRegistry[id]
	return c, ok
}

// GetRegistered returns a copy of the full constructor registry.
func GetRegistered() map[string]PluginConstructor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]PluginConstructor, len(pluginRegistry))
	for k, v := range pluginRegistry {
		out[k] = v
	}
	return out
}

// ResetRegistry clears the constructor registry. Intended for use in tests only.
func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	pluginRegistry = map[string]PluginConstructor{}
}
