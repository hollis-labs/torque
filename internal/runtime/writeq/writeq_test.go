package writeq_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupStore(t *testing.T) *sqlstore.Store {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "torque.db"))
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))

	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func seedTask(t *testing.T, store *sqlstore.Store, id string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       id,
		Title:    "writeq test",
		Status:   "todo",
		Executor: "cli",
	}))
}

func TestQueue_SubmitAppliesStateWrites(t *testing.T) {
	store := setupStore(t)
	seedTask(t, store, "CW-WQ-0001")

	q := writeq.New(store, writeq.Options{
		QueueSize:   8,
		MaxBatch:    4,
		BatchWindow: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = q.Run(ctx) }()

	require.NoError(t, q.Submit(context.Background(), "task_doing", func(tx *sqlstore.WriteTx) error {
		return tx.TransitionTask("CW-WQ-0001", "doing")
	}))
	require.NoError(t, q.Submit(context.Background(), "task_review", func(tx *sqlstore.WriteTx) error {
		return tx.TransitionTask("CW-WQ-0001", "review")
	}))

	task, err := store.GetTask("CW-WQ-0001")
	require.NoError(t, err)
	require.Equal(t, "review", task.Status)

	stats := q.Stats()
	require.EqualValues(t, 2, stats.Submitted)
	require.EqualValues(t, 2, stats.Completed)
}

func TestQueue_SavepointIsolation(t *testing.T) {
	store := setupStore(t)
	seedTask(t, store, "CW-WQ-0002")

	q := writeq.New(store, writeq.Options{
		QueueSize:   8,
		MaxBatch:    8,
		BatchWindow: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = q.Run(ctx) }()

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	errs := make([]error, 2)
	go func() {
		defer wg.Done()
		<-start
		errs[0] = q.Submit(context.Background(), "bad_transition", func(tx *sqlstore.WriteTx) error {
			return tx.TransitionTask("CW-MISSING", "doing")
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs[1] = q.Submit(context.Background(), "good_transition", func(tx *sqlstore.WriteTx) error {
			return tx.TransitionTask("CW-WQ-0002", "doing")
		})
	}()
	close(start)
	wg.Wait()

	require.Error(t, errs[0])
	require.NoError(t, errs[1])

	task, err := store.GetTask("CW-WQ-0002")
	require.NoError(t, err)
	require.Equal(t, "doing", task.Status)

	stats := q.Stats()
	require.EqualValues(t, 2, stats.Submitted)
	require.EqualValues(t, 1, stats.Completed)
	require.EqualValues(t, 1, stats.Failed)
}
