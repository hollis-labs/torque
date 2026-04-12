package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
)

// addTestSubscriber directly installs a buffered subscriber channel on the
// hub's internal map. Test-only helper so bridge tests can observe broadcasts
// without spinning up an HTTP server and parsing SSE frames.
func addTestSubscriber(h *SSEHub) chan SSEEvent {
	ch := make(chan SSEEvent, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func removeTestSubscriber(h *SSEHub, ch chan SSEEvent) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// waitForEvent pulls the next SSEEvent from ch, failing the test on timeout.
func waitForEvent(t *testing.T, ch <-chan SSEEvent, timeout time.Duration) SSEEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for SSE event", timeout)
		return SSEEvent{}
	}
}

func TestSchedulerBridgeForwardsEvents(t *testing.T) {
	t.Parallel()

	bus := scheduler.NewEventBus()
	defer bus.Close()
	hub := NewSSEHub()
	sub := addTestSubscriber(hub)
	defer removeTestSubscriber(hub, sub)

	bridge := NewSchedulerBridge(hub, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(done)
	}()

	// Wait briefly for the bridge to register its subscription on the bus,
	// otherwise the Publish may race ahead of Subscribe.
	deadline := time.Now().Add(time.Second)
	for bus.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.SubscriberCount() == 0 {
		t.Fatal("bridge did not subscribe to bus in time")
	}

	bus.Publish(scheduler.SchedulerEvent{
		Type:   "run.started",
		TaskID: "task-123",
		RunID:  42,
		Data:   map[string]interface{}{"foo": "bar"},
	})

	ev := waitForEvent(t, sub, time.Second)

	if ev.Type != "run.started" {
		t.Errorf("type = %q, want %q", ev.Type, "run.started")
	}
	if got, want := ev.Data["task_id"], "task-123"; got != want {
		t.Errorf("data.task_id = %v, want %v", got, want)
	}
	if got, want := ev.Data["run_id"], int64(42); got != want {
		t.Errorf("data.run_id = %v, want %v", got, want)
	}
	payload, ok := ev.Data["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("data.payload type = %T, want map[string]interface{}", ev.Data["payload"])
	}
	if got, want := payload["foo"], "bar"; got != want {
		t.Errorf("data.payload.foo = %v, want %v", got, want)
	}
	if ev.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit after ctx cancel")
	}
}

func TestSchedulerBridgeShutsDownOnCtxCancel(t *testing.T) {
	t.Parallel()

	bus := scheduler.NewEventBus()
	defer bus.Close()
	hub := NewSSEHub()
	bridge := NewSchedulerBridge(hub, bus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(done)
	}()

	// Wait for subscription so we know Run is actually in its loop.
	deadline := time.Now().Add(time.Second)
	for bus.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("bridge did not return within 500ms of ctx cancel")
	}

	if count := bus.SubscriberCount(); count != 0 {
		t.Errorf("bus subscriber count after bridge shutdown = %d, want 0", count)
	}
}

func TestSchedulerBridgeShutsDownOnBusClose(t *testing.T) {
	t.Parallel()

	bus := scheduler.NewEventBus()
	hub := NewSSEHub()
	bridge := NewSchedulerBridge(hub, bus)

	done := make(chan struct{})
	go func() {
		bridge.Run(context.Background())
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for bus.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	bus.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("bridge did not return within 500ms of bus close")
	}
}

func TestSchedulerBridgeForwardsMultipleEvents(t *testing.T) {
	t.Parallel()

	bus := scheduler.NewEventBus()
	defer bus.Close()
	hub := NewSSEHub()
	sub := addTestSubscriber(hub)
	defer removeTestSubscriber(hub, sub)

	bridge := NewSchedulerBridge(hub, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for bus.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.SubscriberCount() == 0 {
		t.Fatal("bridge did not subscribe to bus in time")
	}

	events := []scheduler.SchedulerEvent{
		{Type: "run.started", TaskID: "task-1", RunID: 1},
		{Type: "run.progress", TaskID: "task-1", RunID: 1, Data: map[string]interface{}{"pct": 50}},
		{Type: "run.completed", TaskID: "task-1", RunID: 1},
	}
	for _, ev := range events {
		bus.Publish(ev)
	}

	for i, want := range events {
		got := waitForEvent(t, sub, time.Second)
		if got.Type != want.Type {
			t.Errorf("event[%d].type = %q, want %q", i, got.Type, want.Type)
		}
		if gotID, wantID := got.Data["task_id"], want.TaskID; gotID != wantID {
			t.Errorf("event[%d].task_id = %v, want %v", i, gotID, wantID)
		}
		if gotRun, wantRun := got.Data["run_id"], want.RunID; gotRun != wantRun {
			t.Errorf("event[%d].run_id = %v, want %v", i, gotRun, wantRun)
		}
	}

	// No extra events.
	select {
	case extra := <-sub:
		t.Errorf("unexpected extra event: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit after ctx cancel")
	}
}

// TestSchedulerBridgeForwardsEventsWithZeroFields ensures events without a
// task or run (e.g. scheduler.tick) still broadcast with empty task_id and
// run_id: 0, so browser filters can ignore them safely.
func TestSchedulerBridgeForwardsEventsWithZeroFields(t *testing.T) {
	t.Parallel()

	bus := scheduler.NewEventBus()
	defer bus.Close()
	hub := NewSSEHub()
	sub := addTestSubscriber(hub)
	defer removeTestSubscriber(hub, sub)

	bridge := NewSchedulerBridge(hub, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for bus.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	bus.Publish(scheduler.SchedulerEvent{Type: "scheduler.tick"})

	ev := waitForEvent(t, sub, time.Second)
	if ev.Type != "scheduler.tick" {
		t.Errorf("type = %q, want %q", ev.Type, "scheduler.tick")
	}
	if got, want := ev.Data["task_id"], ""; got != want {
		t.Errorf("data.task_id = %v, want %q", got, want)
	}
	if got, want := ev.Data["run_id"], int64(0); got != want {
		t.Errorf("data.run_id = %v, want %v", got, want)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit after ctx cancel")
	}
}
