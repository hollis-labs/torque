package agent

import (
	"context"

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

// storeStateSink adapts Dependencies to agentsessions.StateSink. State
// strings are passed through verbatim; the migration's CHECK constraint
// enforces the four-value go-agent-sessions vocabulary plus `crashed`.
//
// Forked from internal/runtime/sessionmgr/sinks.go without semantic change.
type storeStateSink struct {
	deps *Dependencies
}

func (s *storeStateSink) UpdateSessionState(id string, state agentsessions.State, pid int, exit *int) error {
	return s.deps.UpdateSessionState(context.Background(), id, string(state), pid, exit)
}

// busEventSink adapts an EventEmitter to agentsessions.EventSink. Each
// LifecycleEvent fans out as a session.state_changed SSE event with a flat
// data shape matching the run_events bridge.
//
// onTerminal, when non-nil, is invoked with the session ID after the SSE
// event is published — the Manager wires this so per-session loopback
// handles + stderr sidecars are torn down deterministically when the
// underlying session transitions to a terminal state, not just on explicit
// Stop. Idempotent on the Manager side.
type busEventSink struct {
	events     EventEmitter
	onTerminal func(sessID string)
}

func (s *busEventSink) Emit(_ context.Context, ev agentsessions.LifecycleEvent) {
	if s.events != nil {
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
	if s.onTerminal != nil {
		switch agentsessions.State(ev.To) {
		case agentsessions.StateDone, agentsessions.StateFailed:
			s.onTerminal(ev.SessionID)
		}
	}
}
