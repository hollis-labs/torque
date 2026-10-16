package sqlstore_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestRunAttemptsAtomicAndNeverReused(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("attempts")
	require.NoError(t, store.CreateTask(task))
	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	rolledBack := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"}
	_, err = tx.CreateRun(rolledBack)
	require.NoError(t, err)
	require.Equal(t, 1, rolledBack.Attempt)
	require.NoError(t, tx.Rollback())
	first := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", ProgressDetail: sql.NullString{String: "verbose", Valid: true}}
	id, err := store.CreateRun(first)
	require.NoError(t, err)
	require.Equal(t, 1, first.Attempt, "rollback did not reserve an attempt")
	got, err := store.GetRun(id)
	require.NoError(t, err)
	require.Equal(t, first.ProgressDetail, got.ProgressDetail)
	require.Equal(t, 1, got.Attempt)
	_, err = store.DB().Exec("DELETE FROM runs WHERE id = ?", id)
	require.NoError(t, err)

	const count = 12
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", Status: "blocked"}
			if i%2 == 0 {
				_, err := store.CreateRun(r)
				errs <- err
				return
			}
			tx, err := store.BeginWriteTx(context.Background())
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			if _, err = tx.CreateRun(r); err == nil {
				err = tx.Commit()
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	runs, err := store.ListRuns(task.ID)
	require.NoError(t, err)
	require.Len(t, runs, count)
	attempts := map[int]bool{}
	for _, r := range runs {
		require.Greater(t, r.Attempt, 1)
		require.LessOrEqual(t, r.Attempt, count+1)
		require.False(t, attempts[r.Attempt])
		attempts[r.Attempt] = true
	}
	other := sampleTask("other-attempts")
	require.NoError(t, store.CreateTask(other))
	r := &sqlstore.RunRecord{TaskID: other.ID, Executor: "cli"}
	_, err = store.CreateRun(r)
	require.NoError(t, err)
	require.Equal(t, 1, r.Attempt)
}

func TestProgressDetailWriteTxAndSessionRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	detail := sql.NullString{String: "off", Valid: true}
	task := sampleTask("detail")
	task.ProgressDetail, task.ProgressDetailSource = detail, sql.NullString{String: "task", Valid: true}
	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, tx.CreateTask(task))
	require.NoError(t, tx.CreateSession(&sqlstore.SessionRecord{ID: "tx-session", ProgressDetail: detail}))
	require.NoError(t, tx.Commit())
	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	require.Equal(t, detail, got.ProgressDetail)
	require.Equal(t, task.ProgressDetailSource, got.ProgressDetailSource)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "direct-session", ProgressDetail: detail}))
	for _, id := range []string{"tx-session", "direct-session"} {
		sess, err := store.GetSession(id)
		require.NoError(t, err)
		require.Equal(t, detail, sess.ProgressDetail)
	}
}

func TestRunSessionCorrelationRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("correlation")
	require.NoError(t, store.CreateTask(task))
	tether := sql.NullString{String: "tether-session", Valid: true}
	launch := sql.NullString{String: "tether-launch", Valid: true}
	workstream := sql.NullString{String: "tether-workstream", Valid: true}
	run := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", TetherSessionID: tether, LaunchID: launch, WorkstreamID: workstream}
	id, err := store.CreateRun(run)
	require.NoError(t, err)
	got, err := store.GetRun(id)
	require.NoError(t, err)
	require.Equal(t, tether, got.TetherSessionID)
	require.Equal(t, launch, got.LaunchID)
	require.Equal(t, workstream, got.WorkstreamID)
	list, err := store.ListRuns(task.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, got.TetherSessionID, list[0].TetherSessionID)
	for _, sid := range []string{"direct", "tx"} {
		sess := &sqlstore.SessionRecord{ID: sid, RunID: sql.NullInt64{Int64: id, Valid: true}, TetherSessionID: tether, LaunchID: launch, WorkstreamID: workstream}
		if sid == "direct" {
			require.NoError(t, store.CreateSession(sess))
		} else {
			tx, err := store.BeginWriteTx(context.Background())
			require.NoError(t, err)
			defer tx.Rollback()
			require.NoError(t, tx.CreateSession(sess))
			require.NoError(t, tx.Commit())
		}
		persisted, err := store.GetSession(sid)
		require.NoError(t, err)
		require.Equal(t, sess.RunID, persisted.RunID)
		require.Equal(t, tether, persisted.TetherSessionID)
		require.Equal(t, launch, persisted.LaunchID)
		require.Equal(t, workstream, persisted.WorkstreamID)
	}
}
