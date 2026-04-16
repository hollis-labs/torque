// Package waitpoll provides the predicate registry and built-in evaluators
// that drive kind=wait task dispatch in the scheduler. A predicate inspects
// external state (another task's status, a URL, a file, etc.) and reports
// whether its wait condition has been met; the scheduler polls each wait
// task on every tick and transitions it when a predicate fires.
package waitpoll

import "context"

// Predicate is a wait-condition evaluator. Implementations must be safe to
// call concurrently from scheduler goroutines.
//
// Type returns the stable identifier used in metadata.wait.predicate_type
// (e.g. "task_done", "url_reachable").
//
// Validate inspects params at task-creation time so the service layer can
// reject a wait task whose predicate_type is known but whose params are
// malformed. Evaluate is the runtime poll.
type Predicate interface {
	Type() string
	Validate(params map[string]any) error
	Evaluate(ctx context.Context, params map[string]any) (bool, error)
}
