package scheduler_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLifecycleBlocksOnMissingRequiredSubtodos: agent reports done but a
// required subtodo is still unchecked — the task must land in blocked with
// an explanatory reason, not review/done.
func TestLifecycleBlocksOnMissingRequiredSubtodos(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	taskID := "CW-0001"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "gated", Status: "doing", Executor: "cli", OnDone: "review",
	}))
	require.NoError(t, store.SetSubtodos(taskID, []sqlstore.Subtodo{
		{ID: "item-1", Text: "must do", Required: true},
		{ID: "item-2", Text: "optional", Required: false},
	}))

	err := lm.HandleResult(taskID, 1, &executor.ExecutionResult{Status: "done"})
	require.NoError(t, err)

	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "item-1")
}

// TestLifecycleAllowsDoneWhenRequiredSubtodosTicked: once the agent ticks
// every required item via the torque_task_subtodo_done MCP tool, the
// task transitions through the normal OnDone path.
func TestLifecycleAllowsDoneWhenRequiredSubtodosTicked(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	taskID := "CW-0002"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "ok", Status: "doing", Executor: "cli", OnDone: "review",
	}))
	require.NoError(t, store.SetSubtodos(taskID, []sqlstore.Subtodo{
		{ID: "item-1", Text: "done one", Required: true, Done: true, Evidence: "sha-1"},
		{ID: "item-2", Text: "optional unchecked", Required: false, Done: false},
	}))

	err := lm.HandleResult(taskID, 1, &executor.ExecutionResult{Status: "done"})
	require.NoError(t, err)

	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status)
}

// TestLifecycleNoSubtodosBehavesLegacy: tasks without a subtodo list keep
// the existing OnDone behaviour — the new gate must not regress the
// baseline case.
func TestLifecycleNoSubtodosBehavesLegacy(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0003", Title: "no subtodos", Status: "doing", Executor: "cli", OnDone: "close",
	}))

	err := lm.HandleResult("CW-0003", 1, &executor.ExecutionResult{Status: "done"})
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0003")
	assert.Equal(t, "done", task.Status)
}
