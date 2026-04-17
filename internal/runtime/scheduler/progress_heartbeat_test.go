package scheduler

import (
	"testing"
	"time"
)

// drainHeartbeats collects SchedulerEvents of type run.progress with
// kind=heartbeat from sub over d. Non-matching events are ignored so other
// scheduler traffic on the bus doesn't pollute the assertion.
func drainHeartbeats(sub <-chan SchedulerEvent, d time.Duration) []SchedulerEvent {
	deadline := time.After(d)
	var out []SchedulerEvent
	for {
		select {
		case <-deadline:
			return out
		case ev, ok := <-sub:
			if !ok {
				return out
			}
			if ev.Type != "run.progress" {
				continue
			}
			data, _ := ev.Data.(map[string]interface{})
			if data["kind"] == "heartbeat" {
				out = append(out, ev)
			}
		}
	}
}

func TestProgressHeartbeatEmitsAtInterval(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	h := newProgressHeartbeat(bus, 20*time.Millisecond)
	started := time.Now().UTC()
	h.start("task-1", 42, "worker-1", started)
	defer h.stop(42)

	// Collect for ~120ms — expect at least 3 heartbeats for a 20ms interval.
	events := drainHeartbeats(sub, 120*time.Millisecond)
	if len(events) < 3 {
		t.Fatalf("expected >=3 heartbeats in 120ms, got %d", len(events))
	}

	for _, ev := range events {
		if ev.TaskID != "task-1" {
			t.Errorf("heartbeat task_id = %q, want task-1", ev.TaskID)
		}
		if ev.RunID != 42 {
			t.Errorf("heartbeat run_id = %d, want 42", ev.RunID)
		}
		data, ok := ev.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("heartbeat data type = %T, want map[string]interface{}", ev.Data)
		}
		if data["kind"] != "heartbeat" {
			t.Errorf("kind = %v, want heartbeat", data["kind"])
		}
		if data["worker_id"] != "worker-1" {
			t.Errorf("worker_id = %v, want worker-1", data["worker_id"])
		}
		if _, ok := data["elapsed_sec"].(int); !ok {
			t.Errorf("elapsed_sec type = %T, want int", data["elapsed_sec"])
		}
	}
}

func TestProgressHeartbeatStopHaltsEmissions(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	h := newProgressHeartbeat(bus, 15*time.Millisecond)
	h.start("task-1", 7, "worker-1", time.Now().UTC())

	// Let a couple heartbeats land, then stop.
	time.Sleep(40 * time.Millisecond)
	h.stop(7)

	// Drain whatever's in flight.
	drainHeartbeats(sub, 20*time.Millisecond)

	// After stop, no more heartbeats should land for this run.
	post := drainHeartbeats(sub, 80*time.Millisecond)
	if len(post) != 0 {
		t.Fatalf("expected no heartbeats after stop, got %d", len(post))
	}

	if h.activeCount() != 0 {
		t.Errorf("activeCount after stop = %d, want 0", h.activeCount())
	}
}

func TestProgressHeartbeatZeroIntervalDisables(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	h := newProgressHeartbeat(bus, 0)
	h.start("task-1", 1, "worker-1", time.Now().UTC())

	events := drainHeartbeats(sub, 60*time.Millisecond)
	if len(events) != 0 {
		t.Fatalf("expected no heartbeats when interval=0, got %d", len(events))
	}
	if h.activeCount() != 0 {
		t.Errorf("activeCount = %d, want 0 when disabled", h.activeCount())
	}
}

func TestProgressHeartbeatIdempotentStart(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	defer bus.Close()

	h := newProgressHeartbeat(bus, time.Hour)
	h.start("task-1", 5, "worker-1", time.Now().UTC())
	h.start("task-1", 5, "worker-1", time.Now().UTC())
	h.start("task-1", 5, "worker-1", time.Now().UTC())

	if h.activeCount() != 1 {
		t.Fatalf("activeCount after 3 starts = %d, want 1", h.activeCount())
	}
	h.stop(5)
	if h.activeCount() != 0 {
		t.Errorf("activeCount after stop = %d, want 0", h.activeCount())
	}
	// stop is safe on unknown id.
	h.stop(999)
}

func TestProgressHeartbeatElapsedSec(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	h := newProgressHeartbeat(bus, 10*time.Millisecond)
	// Pin the clock: startedAt 30s in the past, now() returns a fixed point.
	anchor := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return anchor.Add(30 * time.Second) }

	h.start("task-1", 99, "worker-1", anchor)
	defer h.stop(99)

	events := drainHeartbeats(sub, 60*time.Millisecond)
	if len(events) == 0 {
		t.Fatal("expected at least one heartbeat")
	}
	for _, ev := range events {
		data := ev.Data.(map[string]interface{})
		if data["elapsed_sec"] != 30 {
			t.Errorf("elapsed_sec = %v, want 30", data["elapsed_sec"])
		}
	}
}
