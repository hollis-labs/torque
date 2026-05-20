package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchedulerTickEmitsDispatchSkippedEvent verifies that when the picker
// records skip decisions on a tick, the scheduler publishes a
// `scheduler.dispatch_skipped` event carrying tick / candidates / picked /
// counts. Manual tasks are the cheapest way to force a non-empty Counts map
// — the picker records SkipReasonManual without touching the executor pool.
// CW-20260519-0126.
func TestSchedulerTickEmitsDispatchSkippedEvent(t *testing.T) {
	sched, store, _ := setupScheduler(t)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	// Manual=true short-circuits in Picker.Pick → record(SkipReasonManual)
	// before any executor / project gating, so the Counts map is
	// guaranteed non-empty regardless of pool state.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-MANUAL-SKIP", Title: "manual task forces skip count",
		Status: "todo", Priority: 1, Manual: true,
		Executor: "mock", AgentProfile: "mock",
	}))

	require.NoError(t, sched.Tick(context.Background()))

	var skip scheduler.SchedulerEvent
	deadline := time.After(2 * time.Second)
	found := false
	for !found {
		select {
		case ev := <-sub:
			if ev.Type == "scheduler.dispatch_skipped" {
				skip = ev
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for scheduler.dispatch_skipped event")
		}
	}

	require.True(t, found, "scheduler.dispatch_skipped must publish when Counts is non-empty")

	data, ok := skip.Data.(map[string]interface{})
	require.True(t, ok, "Data should be map[string]interface{}, got %T", skip.Data)

	// tick / candidates / picked are required scalars; counts is the
	// per-reason histogram (also a map[string]interface{} since the
	// emission site copies ints into interface{} values).
	assert.Contains(t, data, "tick", "payload missing tick")
	assert.Contains(t, data, "candidates", "payload missing candidates")
	assert.Contains(t, data, "picked", "payload missing picked")
	require.Contains(t, data, "counts", "payload missing counts")

	counts, ok := data["counts"].(map[string]interface{})
	require.True(t, ok, "counts should be map[string]interface{}, got %T", data["counts"])
	assert.Contains(t, counts, scheduler.SkipReasonManual,
		"manual-only candidate should surface as SkipReasonManual in counts")

	// candidates must include the one manual task; picked is 0 because
	// the manual task is skipped and no other candidates exist.
	assert.GreaterOrEqual(t, toInt(t, data["candidates"]), 1,
		"candidates should count the manual task we created")
	assert.Equal(t, 0, toInt(t, data["picked"]),
		"no executable candidate exists, so picked must be 0")
}

// TestSchedulerTickSuppressesDispatchSkippedWhenNothingSkipped verifies the
// suppression invariant: when the picker reports zero skips (e.g. no todo
// candidates at all), no `scheduler.dispatch_skipped` event fires. This is
// the "steady-state idle scheduler" gate referenced by the emission comment
// — without it, every tick on an empty queue would publish a noise event.
func TestSchedulerTickSuppressesDispatchSkippedWhenNothingSkipped(t *testing.T) {
	sched, _, _ := setupScheduler(t)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	// No tasks created — picker has zero candidates → Counts stays empty.
	require.NoError(t, sched.Tick(context.Background()))

	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case ev := <-sub:
			if ev.Type == "scheduler.dispatch_skipped" {
				t.Fatalf("dispatch_skipped should not fire on an empty queue, got %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

// toInt coerces an interface{} containing an int (the emission stores
// decisions.Candidates / len(tasks) as native int) for assertion. Kept
// local to this test file — production code never reads the payload back
// as int, only consumers do, and they handle JSON-shaped float64.
func toInt(t *testing.T, v interface{}) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}

