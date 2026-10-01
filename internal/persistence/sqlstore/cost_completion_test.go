package sqlstore_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// completeWithCost runs CompleteRunWithCost in its own write transaction.
func completeWithCost(t *testing.T, store *sqlstore.Store, runID int64, c sqlstore.RunCompletion, ledger *sqlstore.CostLedgerRecord) (bool, error) {
	t.Helper()
	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	updated, err := tx.CompleteRunWithCost(runID, c, ledger)
	if err != nil {
		require.NoError(t, tx.Rollback())
		return updated, err
	}
	require.NoError(t, tx.Commit())
	return updated, nil
}

func ledgerRows(t *testing.T, store *sqlstore.Store, runID int64) (n int, sum float64) {
	t.Helper()
	require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost), 0) FROM cost_ledger WHERE run_id = ?`, runID).Scan(&n, &sum))
	return n, sum
}

func newRunningRun(t *testing.T, store *sqlstore.Store, taskID string) int64 {
	t.Helper()
	require.NoError(t, store.CreateTask(sampleTask(taskID)))
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cli"})
	require.NoError(t, err)
	return id
}

// The run row and its ledger row are written together and agree.
func TestCompleteRunWithCost_WritesTheRunAndItsLedgerRowTogether(t *testing.T) {
	store := setupTestStore(t)
	runID := newRunningRun(t, store, "CW-COST-OK")

	updated, err := completeWithCost(t, store, runID,
		sqlstore.RunCompletion{Status: "done", PromptTokens: 10, Cost: 0.25, CostSource: "provider"},
		&sqlstore.CostLedgerRecord{TaskID: "CW-COST-OK", ProviderCost: 0.25})
	require.NoError(t, err)
	assert.True(t, updated)

	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "done", run.Status)
	n, sum := ledgerRows(t, store, runID)
	assert.Equal(t, 1, n)
	assert.InDelta(t, run.Cost, sum, 1e-9, "runs.cost == the run's ledger sum")
}

// A run an operator already cancelled, superseded or killed keeps its row as
// stamped and gets no ledger row, so the two never disagree.
func TestCompleteRunWithCost_NoLedgerRowForAnOperatorTerminalRun(t *testing.T) {
	for _, status := range []string{"cancelled", "superseded", "killed"} {
		t.Run(status, func(t *testing.T) {
			store := setupTestStore(t)
			runID := newRunningRun(t, store, "CW-COST-OP-"+status)
			_, err := store.DB().Exec(`UPDATE runs SET status = ? WHERE id = ?`, status, runID)
			require.NoError(t, err)

			updated, err := completeWithCost(t, store, runID,
				sqlstore.RunCompletion{Status: "done", Cost: 9.99, CostSource: "provider"},
				&sqlstore.CostLedgerRecord{TaskID: "CW-COST-OP-" + status, ProviderCost: 9.99})
			require.NoError(t, err)
			assert.False(t, updated, "the late executor result does not clobber the operator's status")

			run, err := store.GetRun(runID)
			require.NoError(t, err)
			assert.Equal(t, status, run.Status)
			assert.Zero(t, run.Cost)
			n, _ := ledgerRows(t, store, runID)
			assert.Zero(t, n, "no ledger row for a run that kept its row")
		})
	}
}

// A ledger insert that fails never fails the completion: the run is still
// completed with its cost (stranding it in `running` while its task carried
// on is worse than a missing ledger row), the failed insert leaves nothing
// behind, and the rest of the transaction commits.
func TestCompleteRunWithCost_ALedgerFailureDoesNotStrandTheRun(t *testing.T) {
	store := setupTestStore(t)
	runID := newRunningRun(t, store, "CW-COST-LEDGER-FAIL")

	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	updated, err := tx.CompleteRunWithCost(runID,
		sqlstore.RunCompletion{Status: "done", PromptTokens: 10, Cost: 0.25, CostSource: "provider"},
		// A task that does not exist: the ledger's foreign key refuses the row.
		&sqlstore.CostLedgerRecord{TaskID: "CW-NO-SUCH-TASK", ProviderCost: 0.25})
	require.NoError(t, err, "a ledger failure is logged, not returned")
	assert.True(t, updated)
	// The transaction is still usable after the failed insert.
	_, err = tx.AppendRunEvent(&sqlstore.RunEventRecord{TaskID: "CW-COST-LEDGER-FAIL", Type: "run_completed", Payload: "{}"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "done", run.Status, "the run is not stranded in running")
	assert.InDelta(t, 0.25, run.Cost, 1e-9, "and keeps its cost")
	n, _ := ledgerRows(t, store, runID)
	assert.Zero(t, n, "only the ledger row is missing")
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-COST-LEDGER-FAIL"})
	require.NoError(t, err)
	assert.Len(t, events, 1, "the rest of the transaction committed")
}

// A path that reclaims a run it believes orphaned only writes a run that is
// still running: a run that finished between its check and its write keeps its
// status, tokens and cost, and so keeps agreeing with its ledger row.
func TestCompleteRun_OnlyIfRunningLeavesAFinishedRunAlone(t *testing.T) {
	store := setupTestStore(t)
	runID := newRunningRun(t, store, "CW-REAPER-RACE")
	_, err := completeWithCost(t, store, runID,
		sqlstore.RunCompletion{Status: "done", PromptTokens: 10, Cost: 0.25, CostSource: "provider"},
		&sqlstore.CostLedgerRecord{TaskID: "CW-REAPER-RACE", ProviderCost: 0.25})
	require.NoError(t, err)

	reap := func(onlyIfRunning bool) {
		tx, err := store.BeginWriteTx(context.Background())
		require.NoError(t, err)
		require.NoError(t, tx.CompleteRun(runID, sqlstore.RunCompletion{Status: "failed", ErrorMessage: "orphaned", OnlyIfRunning: onlyIfRunning}))
		require.NoError(t, tx.Commit())
	}

	reap(true)
	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "done", run.Status)
	assert.InDelta(t, 0.25, run.Cost, 1e-9)
	assert.Equal(t, 10, run.PromptTokens)
	_, sum := ledgerRows(t, store, runID)
	assert.InDelta(t, run.Cost, sum, 1e-9, "runs.cost still equals the ledger sum")

	// The control: without the guard the same write resets the run to failed
	// at cost 0 and leaves its ledger row behind, which is the invariant break
	// the guard prevents.
	reap(false)
	run, err = store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status)
	assert.Zero(t, run.Cost)
}

func TestCompleteRun_OnlyIfRunningStillCompletesARunningRun(t *testing.T) {
	store := setupTestStore(t)
	runID := newRunningRun(t, store, "CW-REAPER-OK")
	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	require.NoError(t, tx.CompleteRun(runID, sqlstore.RunCompletion{Status: "failed", ErrorMessage: "orphaned", OnlyIfRunning: true}))
	require.NoError(t, tx.Commit())
	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status)
}
