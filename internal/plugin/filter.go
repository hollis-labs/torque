package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// Filter chain name constants.
const (
	FilterTaskBeforeCreate     = "task.before_create"
	FilterTaskBeforeTransition = "task.before_transition"
	FilterExecutionBeforeRun   = "execution.before_run"
	FilterArtifactBeforeAttach = "artifact.before_attach"
)

type filterEntry struct {
	PluginID string
	Priority int
	Fn       FilterFunc
}

// FilterRegistry manages named filter chains.
type FilterRegistry struct {
	mu     sync.RWMutex
	chains map[string][]filterEntry
}

// NewFilterRegistry creates a new FilterRegistry.
func NewFilterRegistry() *FilterRegistry {
	return &FilterRegistry{
		chains: make(map[string][]filterEntry),
	}
}

// Register adds a filter function to a named chain.
func (r *FilterRegistry) Register(name, pluginID string, priority int, fn FilterFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[name] = append(r.chains[name], filterEntry{
		PluginID: pluginID,
		Priority: priority,
		Fn:       fn,
	})
}

// Apply runs all filters in the named chain in priority order (lower first).
// An error from any filter aborts the chain.
func (r *FilterRegistry) Apply(name string, data interface{}, ctx FilterContext) (interface{}, error) {
	r.mu.RLock()
	entries := make([]filterEntry, len(r.chains[name]))
	copy(entries, r.chains[name])
	r.mu.RUnlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Priority < entries[j].Priority
	})

	var err error
	for _, e := range entries {
		data, err = e.Fn(data, ctx)
		if err != nil {
			return nil, fmt.Errorf("filter %q (plugin %s): %w", name, e.PluginID, err)
		}
	}
	return data, nil
}

// Len returns the number of filters registered for a chain.
func (r *FilterRegistry) Len(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chains[name])
}

// RemoveByPlugin removes all filters registered by a specific plugin.
// Returns the number of entries removed.
func (r *FilterRegistry) RemoveByPlugin(pluginID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for name, entries := range r.chains {
		filtered := entries[:0]
		for _, e := range entries {
			if e.PluginID == pluginID {
				removed++
			} else {
				filtered = append(filtered, e)
			}
		}
		r.chains[name] = filtered
	}
	return removed
}
