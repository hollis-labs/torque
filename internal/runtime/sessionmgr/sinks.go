package sessionmgr

import (
	"context"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// EventEmitter receives session.state_changed lifecycle events. The
// httpserver SchedulerBridge is the production wiring; tests can pass a
// nil EventEmitter or a recording stub.
type EventEmitter interface {
	// EmitSessionEvent publishes the lifecycle transition. The data map
	// follows the run_events SSE convention so existing browser-side
	// listeners can read the same shape.
	EmitSessionEvent(eventType string, data map[string]interface{})
}

// storeStateSink adapts *sqlstore.Store to agentsessions.StateSink.
// State strings are passed through verbatim; the migration's CHECK
// constraint enforces the four-value go-agent-sessions vocabulary plus
// `crashed`.
type storeStateSink struct {
	store *sqlstore.Store
}

// UpdateSessionState persists the inner manager's state transitions.
func (s *storeStateSink) UpdateSessionState(id string, state agentsessions.State, pid int, exit *int) error {
	return s.store.UpdateSessionState(id, string(state), pid, exit)
}

// busEventSink adapts an EventEmitter to agentsessions.EventSink. Each
// LifecycleEvent fans out as an `session.state_changed` SSE event with a
// flat data shape matching the run_events bridge.
type busEventSink struct {
	events EventEmitter
}

// Emit forwards a lifecycle event to the underlying emitter.
func (s *busEventSink) Emit(_ context.Context, ev agentsessions.LifecycleEvent) {
	if s.events == nil {
		return
	}
	data := map[string]interface{}{
		"session_id": ev.SessionID,
		"from":       string(ev.From),
		"to":         string(ev.To),
	}
	if ev.ExitCode != nil {
		data["exit_code"] = *ev.ExitCode
	}
	if ev.Reason != "" {
		data["reason"] = ev.Reason
	}
	s.events.EmitSessionEvent(string(ev.Kind), data)
}
