package httpserver

import (
	"context"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// SchedulerBridge subscribes to a scheduler.EventBus and republishes each event
// to an SSEHub so HTTP SSE clients receive it. Path A forwards all events
// without filtering — clients filter on the browser side by task_id / run_id.
//
// A bridge is typically constructed once at serve startup and its Run method
// is launched in a goroutine alongside the scheduler. Run blocks until the
// passed context is cancelled or the underlying EventBus is closed.
type SchedulerBridge struct {
	hub *SSEHub
	bus *scheduler.EventBus
}

// NewSchedulerBridge returns a bridge ready to be Run. Subscription to the
// bus happens inside Run, not here — the constructor is side-effect free.
func NewSchedulerBridge(hub *SSEHub, bus *scheduler.EventBus) *SchedulerBridge {
	return &SchedulerBridge{hub: hub, bus: bus}
}

// Run subscribes to the bus and forwards events to the hub until ctx is
// cancelled or the bus closes. It is safe to call Run once per bridge; calling
// it multiple times concurrently is not supported.
//
// Forwarded SSE event shape:
//
//	{
//	  "type":      "<SchedulerEvent.Type>",
//	  "data": {
//	     "task_id": "<TaskID>",
//	     "run_id":  <RunID>,
//	     "payload": <SchedulerEvent.Data>,
//	  },
//	  "timestamp": "<RFC3339 from SSEHub>",
//	}
//
// Zero values for TaskID / RunID pass through (events without a task or run,
// e.g. scheduler.tick, still broadcast with empty task_id and run_id: 0).
func (b *SchedulerBridge) Run(ctx context.Context) {
	sub := b.bus.Subscribe()
	defer b.bus.Unsubscribe(sub)

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub:
			if !ok {
				// Bus closed — channel has been closed by EventBus.Close().
				return
			}
			b.hub.Broadcast(evt.Type, map[string]interface{}{
				"task_id": evt.TaskID,
				"run_id":  evt.RunID,
				"payload": evt.Data,
			})
		}
	}
}
