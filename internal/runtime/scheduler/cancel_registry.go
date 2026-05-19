package scheduler

import (
	"context"
	"sync"
)

// cancelRegistry keeps the per-task CancelFunc for every in-flight worker.
// When a DB-driven transition moves a task out of "doing", the scheduler
// looks up the taskID here and calls cancel so the worker's context
// terminates within one tick (CW-20260418-0005).
//
// Concurrent model:
//   - register / deregister / cancel are all O(1) under a single mutex.
//   - deregister is idempotent; a completion-path deregister and a transition
//     cancel racing each other both exit without error. Whichever wins the
//     map delete is fine — the CancelFunc itself is idempotent in Go.
//   - cancelAll is called from Scheduler.Stop so no workers leak at shutdown.
type cancelRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newCancelRegistry() *cancelRegistry {
	return &cancelRegistry{cancels: make(map[string]context.CancelFunc)}
}

// register stores the cancel function for taskID. If a cancel for the same
// taskID is already registered, the previous one is invoked (defensive —
// the scheduler's own dispatch path is serialized per-task so in practice
// this branch is unreachable, but it prevents an accidental leak if a
// future caller double-registers).
func (r *cancelRegistry) register(taskID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.cancels[taskID]; ok {
		prev()
	}
	r.cancels[taskID] = cancel
}

// deregister removes the cancel function without invoking it. Called from
// the worker's defer after the work has completed naturally. Returns true
// if an entry was removed.
func (r *cancelRegistry) deregister(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cancels[taskID]; ok {
		delete(r.cancels, taskID)
		return true
	}
	return false
}

// cancel invokes the CancelFunc for taskID (if registered) and removes the
// entry. Returns true if a cancel was invoked. Idempotent: calling twice
// for the same taskID returns false on the second call.
func (r *cancelRegistry) cancel(taskID string) bool {
	r.mu.Lock()
	fn, ok := r.cancels[taskID]
	if ok {
		delete(r.cancels, taskID)
	}
	r.mu.Unlock()
	if ok {
		fn()
	}
	return ok
}

// has reports whether a cancel function is currently registered for taskID.
// Used by the stale-heartbeat sweep as a liveness check: a heartbeat row
// whose task is still in the registry means the worker is alive in this
// scheduler process, just not producing executor events fast enough to
// keep its DB heartbeat fresh (CW-20260519-0079 false-positive #2:
// run 904 mid-`go test`).
func (r *cancelRegistry) has(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[taskID]
	return ok
}

// cancelAll invokes every registered cancel and clears the map. Called by
// Scheduler.Stop so no worker leaks through a shutdown. Safe to call on
// an already-empty registry.
func (r *cancelRegistry) cancelAll() {
	r.mu.Lock()
	fns := make([]context.CancelFunc, 0, len(r.cancels))
	for _, fn := range r.cancels {
		fns = append(fns, fn)
	}
	r.cancels = make(map[string]context.CancelFunc)
	r.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// size returns the number of currently-registered cancel functions. Used
// by tests to assert the registry is emptied on worker completion.
func (r *cancelRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancels)
}

// mergeContexts returns a context that is cancelled as soon as EITHER parent
// is cancelled. The cancel func must always be called to release the
// watcher goroutine; put it in a defer at the call site.
//
// Used by dispatchTask to combine pool-shutdown cancellation with the
// per-task dispatch cancellation, so exec.Run receives a single ctx that
// correctly reflects both sources of cancellation.
func mergeContexts(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-ctx.Done():
			// a was cancelled (or cancel was called directly); nothing
			// more to do — watcher exits so no goroutine leaks.
		}
	}()
	return ctx, cancel
}
