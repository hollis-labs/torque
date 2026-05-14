package agent

import (
	"time"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// SchedulerEmitter adapts *scheduler.EventBus to the EventEmitter interface
// so session lifecycle transitions ride the same SSE machinery as run_events.
// Each session.state_changed publish lands on every subscriber the
// SchedulerBridge is forwarding to httpserver's SSEHub.
//
// Forked from internal/runtime/sessionmgr/bus_adapter.go.
type SchedulerEmitter struct {
	Bus *scheduler.EventBus
}

// NewSchedulerEmitter returns an EventEmitter backed by the given bus. Nil
// bus produces a nil-safe emitter that drops events.
func NewSchedulerEmitter(bus *scheduler.EventBus) *SchedulerEmitter {
	return &SchedulerEmitter{Bus: bus}
}

// EmitSessionEvent publishes the lifecycle transition through the bus. data
// is wrapped under SchedulerEvent.Data; TaskID is opportunistically extracted
// from the data map so the SchedulerBridge's task-scoped dashboards can
// correlate.
func (e *SchedulerEmitter) EmitSessionEvent(eventType string, data map[string]interface{}) {
	if e == nil || e.Bus == nil {
		return
	}
	ev := scheduler.SchedulerEvent{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}
	if data != nil {
		if v, ok := data["task_id"].(string); ok {
			ev.TaskID = v
		}
	}
	e.Bus.Publish(ev)
}
