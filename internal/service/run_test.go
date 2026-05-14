package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// TestRunCancel covers the operator-cancel service API (CW-20260418-0015):
// transitions running → cancelled, records the reason in error_message,
// and refuses to transition already-terminal runs.
func TestRunCancel(t *testing.T) {
	svc, store := setupServiceWithStore(t)

	// Seed a task directly via the store so we don't depend on service-level
	// feature flags or validation — the runs-taxonomy path is what's under
	// test, not task creation.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-RUNCANCEL-0001", Title: "t", Status: "doing", Executor: "cli",
	}))

	t.Run("running run cancels cleanly", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUNCANCEL-0001", Executor: "cli"})
		require.NoError(t, err)

		err = svc.Run.Cancel(id, "operator halted for triage")
		require.NoError(t, err)

		got, err := svc.Run.Get(id)
		require.NoError(t, err)
		assert.Equal(t, sqlstore.RunStatusCancelled, got.Status)
		assert.Equal(t, "operator halted for triage", got.ErrorMessage)
		assert.True(t, got.EndedAt.Valid)
	})

	t.Run("already-terminal run is rejected", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUNCANCEL-0001", Executor: "cli"})
		require.NoError(t, err)
		require.NoError(t, store.CompleteRun(id, sqlstore.RunCompletion{
			Status: sqlstore.RunStatusDone,
		}))

		err = svc.Run.Cancel(id, "too late")
		require.Error(t, err, "cancelling a done run must not succeed")
		assert.Contains(t, err.Error(), "already terminal")

		got, _ := svc.Run.Get(id)
		assert.Equal(t, sqlstore.RunStatusDone, got.Status, "terminal state preserved")
	})

	t.Run("double-cancel is rejected", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUNCANCEL-0001", Executor: "cli"})
		require.NoError(t, err)
		require.NoError(t, svc.Run.Cancel(id, "first cancel"))

		err = svc.Run.Cancel(id, "second cancel")
		require.Error(t, err)

		got, _ := svc.Run.Get(id)
		assert.Equal(t, "first cancel", got.ErrorMessage, "first reason preserved")
	})
}

// TestRunKill covers the scheduler-kill service API
// (live-validation cleanup, shutdown cull).
func TestRunKill(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-RUNKILL-0001", Title: "t", Status: "doing", Executor: "cli",
	}))

	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUNKILL-0001", Executor: "cli"})
	require.NoError(t, err)

	err = svc.Run.Kill(id, "live-validation cleanup")
	require.NoError(t, err)

	got, err := svc.Run.Get(id)
	require.NoError(t, err)
	assert.Equal(t, sqlstore.RunStatusKilled, got.Status)
	assert.Equal(t, "live-validation cleanup", got.ErrorMessage)
}

// TestRunSupersede covers the operator-driven supersede service API.
// Distinct from the lifecycle-internal markRunSuperseded path (late result
// on a terminal task): this is the formal operator surface callable from
// HTTP/MCP. For the sibling lifecycle path coverage see
// TestLifecycleRunErrorsWhileTaskTerminalDone.
func TestRunSupersede(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-RUNSUP-0001", Title: "t", Status: "doing", Executor: "cli",
	}))

	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-RUNSUP-0001", Executor: "cli"})
	require.NoError(t, err)

	err = svc.Run.Supersede(id, "accepted via run 42 (commit fb1ec56)")
	require.NoError(t, err)

	got, err := svc.Run.Get(id)
	require.NoError(t, err)
	assert.Equal(t, sqlstore.RunStatusSuperseded, got.Status)
	assert.Equal(t, "accepted via run 42 (commit fb1ec56)", got.ErrorMessage)
}
