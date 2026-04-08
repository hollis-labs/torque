package tool

import "sync"

// Registry is a thread-safe, insertion-ordered collection of Tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string // insertion order (first registration wins ordering slot)
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds t to the registry. If a tool with the same name already
// exists it is replaced in-place (preserving its insertion-order position).
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

// RegisterAll registers each tool in the slice.
func (r *Registry) RegisterAll(tools []Tool) {
	for _, t := range tools {
		r.Register(t)
	}
}

// Get returns the tool with the given name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// GetByNames returns tools for the requested names, skipping any that are
// not registered. Order follows the requested names slice.
func (r *Registry) GetByNames(names []string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// All returns all tools in insertion order.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// ByCategory returns all tools whose Category() matches category, in
// insertion order.
func (r *Registry) ByCategory(category string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Tool
	for _, name := range r.order {
		t := r.tools[name]
		if t.Category() == category {
			out = append(out, t)
		}
	}
	return out
}

// ByTag returns all tools that include tag in their Tags(), in insertion order.
func (r *Registry) ByTag(tag string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Tool
	for _, name := range r.order {
		t := r.tools[name]
		for _, tg := range t.Tags() {
			if tg == tag {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
