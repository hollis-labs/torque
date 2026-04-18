package sqlstore_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndListRuns(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	r1 := &sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: "cli",
	}
	id1, err := store.CreateRun(r1)
	require.NoError(t, err)
	assert.Greater(t, id1, int64(0))
	assert.Equal(t, "running", r1.Status)

	r2 := &sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: "agent",
		Status:   "done",
	}
	id2, err := store.CreateRun(r2)
	require.NoError(t, err)
	assert.Greater(t, id2, id1)

	runs, err := store.ListRuns(task.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	// newest first
	assert.Equal(t, id2, runs[0].ID)
	assert.Equal(t, id1, runs[1].ID)
}

func TestCompleteRun(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	r := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"}
	id, err := store.CreateRun(r)
	require.NoError(t, err)

	exitCode := 0
	err = store.CompleteRun(id, sqlstore.RunCompletion{
		Status:           "done",
		PromptTokens:     100,
		CompletionTokens: 200,
		Cost:             0.05,
		ExitCode:         &exitCode,
		ErrorMessage:     "",
	})
	require.NoError(t, err)

	got, err := store.GetRun(id)
	require.NoError(t, err)
	assert.Equal(t, "done", got.Status)
	assert.Equal(t, 100, got.PromptTokens)
	assert.Equal(t, 200, got.CompletionTokens)
	assert.InDelta(t, 0.05, got.Cost, 0.0001)
	assert.True(t, got.EndedAt.Valid)
	assert.True(t, got.ExitCode.Valid)
	assert.Equal(t, int64(0), got.ExitCode.Int64)
}

func TestGetRun_NotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetRun(9999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestListRunsFiltered exercises the aggregate query path used by the
// Ops dashboard: cross-task listing, status/since/project filters, and
// the limit cap. Each sub-test seeds the minimum data it needs to avoid
// cross-test coupling.
func TestListRunsFiltered(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "proj-a", Name: "A"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "proj-b", Name: "B"}))

	taskA := sampleTask("CW-A-0001")
	taskA.ProjectID = sql.NullString{String: "proj-a", Valid: true}
	require.NoError(t, store.CreateTask(taskA))

	taskB := sampleTask("CW-B-0001")
	taskB.ProjectID = sql.NullString{String: "proj-b", Valid: true}
	require.NoError(t, store.CreateTask(taskB))

	mkRun := func(taskID, status string, execID string) int64 {
		r := &sqlstore.RunRecord{TaskID: taskID, Executor: execID, Status: status}
		id, err := store.CreateRun(r)
		require.NoError(t, err)
		// Ensure distinct started_at ordering — CreateRun stamps UTC now,
		// but consecutive inserts can land in the same millisecond on fast
		// machines. Sleep 1ms so ORDER BY started_at DESC is deterministic.
		time.Sleep(time.Millisecond)
		return id
	}

	rA1 := mkRun(taskA.ID, "running", "cli")
	rA2 := mkRun(taskA.ID, "done", "agent")
	rB1 := mkRun(taskB.ID, "failed", "cli")
	rB2 := mkRun(taskB.ID, "done", "cli")

	t.Run("no filter returns all newest-first", func(t *testing.T) {
		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{})
		require.NoError(t, err)
		require.Len(t, runs, 4)
		assert.Equal(t, rB2, runs[0].ID)
		assert.Equal(t, rB1, runs[1].ID)
		assert.Equal(t, rA2, runs[2].ID)
		assert.Equal(t, rA1, runs[3].ID)
	})

	t.Run("limit caps results", func(t *testing.T) {
		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{Limit: 2})
		require.NoError(t, err)
		require.Len(t, runs, 2)
		assert.Equal(t, rB2, runs[0].ID)
		assert.Equal(t, rB1, runs[1].ID)
	})

	t.Run("task_id pins to single task", func(t *testing.T) {
		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{TaskID: taskA.ID})
		require.NoError(t, err)
		require.Len(t, runs, 2)
		for _, r := range runs {
			assert.Equal(t, taskA.ID, r.TaskID)
		}
	})

	t.Run("status filter accepts multiple", func(t *testing.T) {
		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{Statuses: []string{"done", "failed"}})
		require.NoError(t, err)
		require.Len(t, runs, 3)
		for _, r := range runs {
			assert.Contains(t, []string{"done", "failed"}, r.Status)
		}
	})

	t.Run("project_id joins via tasks", func(t *testing.T) {
		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{ProjectID: "proj-a"})
		require.NoError(t, err)
		require.Len(t, runs, 2)
		for _, r := range runs {
			assert.Equal(t, taskA.ID, r.TaskID)
		}
	})

	t.Run("since drops older runs", func(t *testing.T) {
		// Capture a cutoff between rA2 and rB1, then assert only the
		// runs strictly newer than A2 survive. We look up A2's actual
		// started_at (CreateRun stamps its own time) rather than
		// guessing an offset.
		a2, err := store.GetRun(rA2)
		require.NoError(t, err)
		cutoff := a2.StartedAt.Add(500 * time.Microsecond)

		runs, err := store.ListRunsFiltered(sqlstore.RunFilter{Since: cutoff})
		require.NoError(t, err)
		require.Len(t, runs, 2)
		assert.Equal(t, rB2, runs[0].ID)
		assert.Equal(t, rB1, runs[1].ID)
	})
}

// TestSetRunOperatorStatus covers the runs-taxonomy store helper
// (CW-20260418-0015): operator-terminal statuses stamp the run row with
// the caller-supplied reason and rejected statuses error without writing.
func TestSetRunOperatorStatus(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-OPSTATUS-0001")
	require.NoError(t, store.CreateTask(task))

	t.Run("cancelled stamps status and reason", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
		require.NoError(t, err)

		err = store.SetRunOperatorStatus(id, sqlstore.RunStatusCancelled, "operator halted")
		require.NoError(t, err)

		got, err := store.GetRun(id)
		require.NoError(t, err)
		assert.Equal(t, sqlstore.RunStatusCancelled, got.Status)
		assert.Equal(t, "operator halted", got.ErrorMessage)
		assert.True(t, got.EndedAt.Valid, "ended_at should be stamped")
	})

	t.Run("killed stamps status", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
		require.NoError(t, err)

		err = store.SetRunOperatorStatus(id, sqlstore.RunStatusKilled, "live-validation cleanup")
		require.NoError(t, err)

		got, err := store.GetRun(id)
		require.NoError(t, err)
		assert.Equal(t, sqlstore.RunStatusKilled, got.Status)
		assert.Equal(t, "live-validation cleanup", got.ErrorMessage)
	})

	t.Run("superseded stamps status", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
		require.NoError(t, err)

		err = store.SetRunOperatorStatus(id, sqlstore.RunStatusSuperseded, "accepted via run 42")
		require.NoError(t, err)

		got, err := store.GetRun(id)
		require.NoError(t, err)
		assert.Equal(t, sqlstore.RunStatusSuperseded, got.Status)
	})

	t.Run("rejects non-operator status", func(t *testing.T) {
		id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
		require.NoError(t, err)

		err = store.SetRunOperatorStatus(id, sqlstore.RunStatusFailed, "boom")
		require.Error(t, err, "failed is not an operator-terminal status")
	})

	t.Run("unknown id errors", func(t *testing.T) {
		err := store.SetRunOperatorStatus(99999, sqlstore.RunStatusCancelled, "ghost")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// TestCompleteRunGuardsOperatorTerminalStatus proves that CompleteRun will
// NOT overwrite a run already stamped with an operator-terminal status
// (cancelled/superseded/killed). A late-arriving executor result cannot
// flip operator housekeeping to failed (CW-20260418-0015).
func TestCompleteRunGuardsOperatorTerminalStatus(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-GUARD-0001")
	require.NoError(t, store.CreateTask(task))

	cases := []struct {
		name    string
		opState string
	}{
		{"cancelled is locked", sqlstore.RunStatusCancelled},
		{"superseded is locked", sqlstore.RunStatusSuperseded},
		{"killed is locked", sqlstore.RunStatusKilled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
			require.NoError(t, err)

			require.NoError(t, store.SetRunOperatorStatus(id, tc.opState, "operator wins"))

			// Late-arriving executor result: should be a silent no-op.
			err = store.CompleteRun(id, sqlstore.RunCompletion{
				Status:       sqlstore.RunStatusFailed,
				ErrorMessage: "ghost failure",
			})
			require.NoError(t, err, "CompleteRun must be a silent no-op when operator-terminal")

			got, err := store.GetRun(id)
			require.NoError(t, err)
			assert.Equal(t, tc.opState, got.Status, "operator status must not be clobbered")
			assert.Equal(t, "operator wins", got.ErrorMessage, "operator reason must not be clobbered")
		})
	}
}

// TestCompleteRunStillWorksOnRunning ensures the guard added in
// CW-20260418-0015 did NOT regress the happy path — a running run still
// transitions cleanly to failed/done.
func TestCompleteRunStillWorksOnRunning(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-HAPPY-0001")
	require.NoError(t, store.CreateTask(task))

	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
	require.NoError(t, err)

	err = store.CompleteRun(id, sqlstore.RunCompletion{
		Status:       sqlstore.RunStatusFailed,
		ErrorMessage: "real failure",
	})
	require.NoError(t, err)

	got, err := store.GetRun(id)
	require.NoError(t, err)
	assert.Equal(t, sqlstore.RunStatusFailed, got.Status)
	assert.Equal(t, "real failure", got.ErrorMessage)
}
