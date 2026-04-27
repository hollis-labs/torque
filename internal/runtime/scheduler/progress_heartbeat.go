package scheduler

import (
	"context"
	"sync"
	"time"
)

// progressHeartbeat emits synthetic run.progress {kind:heartbeat} events at a
// fixed interval while a run is active. It backstops the Activity panel so
// consumers see liveness even when an executor isn't producing mid-stream
// events (e.g. an agent that streams nothing user-visible until the final
// turn, or a quiet long-running tool call).
//
// One goroutine is started per active run on start() and torn down on stop().
// start/stop are idempotent and safe to call concurrently.
type progressHeartbeat struct {
	bus      *EventBus
	interval time.Duration
	now      func() time.Time

	mu     sync.Mutex
	active map[int64]context.CancelFunc
}

func newProgressHeartbeat(bus *EventBus, interval time.Duration) *progressHeartbeat {
	return &progressHeartbeat{
		bus:      bus,
		interval: interval,
		now:      time.Now,
		active:   make(map[int64]context.CancelFunc),
	}
}

// start launches a heartbeat goroutine for runID. A non-positive interval
// disables heartbeats entirely (tests and operators can opt out by setting
// CLOCKWORK_PROGRESS_HEARTBEAT_SECONDS=0). startedAt is the anchor for
// elapsed_sec in the emitted payload.
func (h *progressHeartbeat) start(taskID string, runID int64, workerID string, startedAt time.Time) {
	if h.interval <= 0 || h.bus == nil {
		return
	}
	h.mu.Lock()
	if _, exists := h.active[runID]; exists {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.active[runID] = cancel
	h.mu.Unlock()

	go h.loop(ctx, taskID, runID, workerID, startedAt)
}

// stop cancels the heartbeat goroutine for runID. Safe to call on an unknown
// runID (no-op) and safe to call multiple times.
func (h *progressHeartbeat) stop(runID int64) {
	h.mu.Lock()
	cancel, ok := h.active[runID]
	if ok {
		delete(h.active, runID)
	}
	h.mu.Unlock()
	if ok {
		cancel()
	}
}

// activeCount reports how many runs currently have live heartbeat goroutines.
// Used by tests to assert start/stop wiring.
func (h *progressHeartbeat) activeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.active)
}

func (h *progressHeartbeat) loop(ctx context.Context, taskID string, runID int64, workerID string, startedAt time.Time) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	publish := func() {
		elapsed := int(h.now().Sub(startedAt) / time.Second)
		if elapsed < 0 {
			elapsed = 0
		}
		h.bus.Publish(SchedulerEvent{
			Type:   "run.progress",
			TaskID: taskID,
			RunID:  runID,
			Data: map[string]interface{}{
				"kind":        "heartbeat",
				"elapsed_sec": elapsed,
				"worker_id":   workerID,
			},
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
