package executor

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps executor names to Executor instances.
type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

// NewRegistry creates an empty executor registry.
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

// Register adds an executor to the registry, keyed by its Name().
func (r *Registry) Register(exec Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[exec.Name()] = exec
}

// Get returns the executor with the given name, or an error if not found.
func (r *Registry) Get(name string) (Executor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exec, ok := r.executors[name]
	if !ok {
		return nil, fmt.Errorf("executor %q not found", name)
	}
	return exec, nil
}

// List returns sorted names of all registered executors.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.executors))
	for name := range r.executors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
