package sqlstore_test

import (
	"sync"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskTransitionHook_EmittedOnTransition verifies that registered hooks
// receive an event with accurate old/new status after a successful
// TransitionTask. (CW-20260418-0005)
func TestTaskTransitionHook_EmittedOnTransition(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-HOOK-0001")))

	var mu sync.Mutex
	var events []sqlstore.TaskTransitionEvent
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})

	require.NoError(t, store.TransitionTask("CW-HOOK-0001", "doing"))
	require.NoError(t, store.TransitionTask("CW-HOOK-0001", "review"))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 2)
	assert.Equal(t, "CW-HOOK-0001", events[0].TaskID)
	assert.Equal(t, "todo", events[0].OldStatus)
	assert.Equal(t, "doing", events[0].NewStatus)
	assert.Equal(t, "", events[0].Reason)

	assert.Equal(t, "doing", events[1].OldStatus)
	assert.Equal(t, "review", events[1].NewStatus)
}

// TestTaskTransitionHook_ReasonPopulated verifies that
// TransitionTaskWithReason routes the reason through to the hook event.
func TestTaskTransitionHook_ReasonPopulated(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTask(sampleTask("CW-HOOK-0002")))
	require.NoError(t, store.TransitionTask("CW-HOOK-0002", "doing"))

	var got sqlstore.TaskTransitionEvent
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		got = ev
	})

	require.NoError(t, store.TransitionTaskWithReason("CW-HOOK-0002", "blocked", "need input"))

	assert.Equal(t, "CW-HOOK-0002", got.TaskID)
	assert.Equal(t, "doing", got.OldStatus)
	assert.Equal(t, "blocked", got.NewStatus)
	assert.Equal(t, "need input", got.Reason)
}

// TestTaskTransitionHook_NotFound ensures no hook fires on failed transitions.
func TestTaskTransitionHook_NotFound(t *testing.T) {
	store := setupTestStore(t)

	var called bool
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		called = true
	})

	err := store.TransitionTask("CW-DOES-NOT-EXIST", "doing")
	require.Error(t, err)
	assert.False(t, called, "hook must not fire when transition fails")
}

// TestTaskTransitionHook_ParkFires verifies ParkTaskOnCheckpoint emits a
// doing → review hook when its conditional UPDATE actually matches.
func TestTaskTransitionHook_ParkFires(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-HOOK-0003")
	task.CheckpointMode = "blocking"
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.TransitionTask(task.ID, "doing"))

	var events []sqlstore.TaskTransitionEvent
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		events = append(events, ev)
	})

	parked, err := store.ParkTaskOnCheckpoint(task.ID, "awaiting review")
	require.NoError(t, err)
	require.True(t, parked)

	require.Len(t, events, 1)
	assert.Equal(t, "doing", events[0].OldStatus)
	assert.Equal(t, "review", events[0].NewStatus)
	assert.Equal(t, "awaiting review", events[0].Reason)
}

// TestTaskTransitionHook_ParkNoopNoEvent — conditional UPDATE misses, no hook.
func TestTaskTransitionHook_ParkNoopNoEvent(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-HOOK-0004")
	// checkpoint_mode defaults to "none" so the predicate won't match.
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.TransitionTask(task.ID, "doing"))

	var events []sqlstore.TaskTransitionEvent
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		events = append(events, ev)
	})

	parked, err := store.ParkTaskOnCheckpoint(task.ID, "unused")
	require.NoError(t, err)
	assert.False(t, parked)
	assert.Empty(t, events, "hook must not fire when park predicate misses")
}
