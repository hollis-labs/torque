package waitpoll

import (
	"fmt"
	"sync"
)

// Registry holds the active predicates keyed by their Type(). It's
// goroutine-safe so the scheduler tick can look up predicates without
// coordinating with bootstrap.
type Registry struct {
	mu    sync.RWMutex
	preds map[string]Predicate
}

// NewRegistry returns an empty predicate registry.
func NewRegistry() *Registry {
	return &Registry{preds: map[string]Predicate{}}
}

// Register adds a predicate. Duplicate types fail — bootstrap owns the
// canonical set, and accidental double-registration is an error worth
// surfacing loudly rather than silently replacing.
func (r *Registry) Register(p Predicate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.preds[p.Type()]; exists {
		return fmt.Errorf("predicate %q already registered", p.Type())
	}
	r.preds[p.Type()] = p
	return nil
}

// Get returns the predicate registered under typ, or ok=false if unknown.
func (r *Registry) Get(typ string) (Predicate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.preds[typ]
	return p, ok
}

// List returns the registered predicate types in unspecified order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.preds))
	for k := range r.preds {
		out = append(out, k)
	}
	return out
}
