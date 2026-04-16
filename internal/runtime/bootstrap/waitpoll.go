package bootstrap

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

// Waitpoll registers the built-in wait predicates — task_done (backed by
// the store), url_reachable, and file_exists — with the given registry.
// Returns an error on the first duplicate-registration or nil-store
// problem; callers should abort startup rather than continuing with a
// half-initialised registry.
func Waitpoll(reg *waitpoll.Registry, store *sqlstore.Store) error {
	if reg == nil {
		return fmt.Errorf("waitpoll bootstrap: registry is nil")
	}
	if store == nil {
		return fmt.Errorf("waitpoll bootstrap: store is nil (task_done needs it)")
	}
	if err := reg.Register(waitpoll.NewTaskDone(store)); err != nil {
		return fmt.Errorf("register task_done: %w", err)
	}
	if err := reg.Register(waitpoll.NewURLReachable(nil)); err != nil {
		return fmt.Errorf("register url_reachable: %w", err)
	}
	if err := reg.Register(waitpoll.NewFileExists()); err != nil {
		return fmt.Errorf("register file_exists: %w", err)
	}
	return nil
}
