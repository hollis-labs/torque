package sqlstore

// TaskTransitionEvent is fired after a task's status column is successfully
// updated by TransitionTask or TransitionTaskWithReason. Subscribers are
// invoked synchronously in registration order; handlers must be non-blocking
// and must NOT re-enter store methods that themselves emit transitions.
//
// This primitive is intentionally minimal. It exists so the scheduler can
// observe DB-driven transitions out of "doing" and cancel the corresponding
// in-flight worker (CW-20260418-0005). The 48H runs-taxonomy sprint
// (CW-20260418-0015) may normalize this into a richer event bus at merge
// time; keep the surface area small so reconciliation is cheap.
type TaskTransitionEvent struct {
	TaskID    string
	OldStatus string
	NewStatus string
	// Reason is populated by TransitionTaskWithReason; empty for plain
	// TransitionTask. Subscribers should treat empty as "no reason given"
	// rather than assuming a default.
	Reason string
}

// TaskTransitionHook is invoked for each successful status transition.
type TaskTransitionHook func(TaskTransitionEvent)

// RegisterTaskTransitionHook appends a hook to the subscriber list. Hooks
// are never removed — the scheduler (the only current subscriber) lives for
// the life of the process. If future callers need unregister semantics,
// switch to a slice-of-pointers with tombstones; don't pretend this is a
// full pub-sub.
func (s *Store) RegisterTaskTransitionHook(fn TaskTransitionHook) {
	s.hooksMu.Lock()
	defer s.hooksMu.Unlock()
	s.transitionHooks = append(s.transitionHooks, fn)
}

// emitTaskTransition fans an event out to every registered hook. Called by
// TransitionTask and TransitionTaskWithReason after a confirmed-successful
// UPDATE. Hooks run inside the RLock so concurrent Register calls don't race
// with iteration; in practice the only subscriber is the scheduler and its
// body is a map lookup + context cancel, so there's no need to copy-and-release.
func (s *Store) emitTaskTransition(ev TaskTransitionEvent) {
	s.hooksMu.RLock()
	defer s.hooksMu.RUnlock()
	for _, h := range s.transitionHooks {
		h(ev)
	}
}
