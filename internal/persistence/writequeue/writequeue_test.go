package writequeue_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/writequeue"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupWriter(t *testing.T, cfg writequeue.Config) (*sqlstore.Store, *writequeue.Writer, *sql.DB) {
	t.Helper()

	dir := t.TempDir()
	mainPath := filepath.Join(dir, "clockwork.db")
	mainDB, err := sql.Open("sqlite", mainPath)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(mainDB))

	store, err := sqlstore.New(mainDB, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	task := &sqlstore.TaskRecord{
		ID:       "CW-20260510-0113",
		Title:    "queue test",
		Status:   "todo",
		Executor: "cli",
	}
	require.NoError(t, store.CreateTask(task))

	queuePath := filepath.Join(dir, "queue.db")
	queueDB, err := writequeue.OpenDB(context.Background(), queuePath)
	require.NoError(t, err)

	writer, err := writequeue.New(store, queueDB, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, writer.Close()) })

	return store, writer, queueDB
}

func TestWriter_DrainsRunEventsAndCost(t *testing.T) {
	store, writer, _ := setupWriter(t, writequeue.DefaultConfig())

	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID:   "CW-20260510-0113",
		Executor: "cli",
		Status:   "running",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = writer.Start(ctx) }()

	require.NoError(t, writer.AppendRunEvent(context.Background(), &sqlstore.RunEventRecord{
		RunID:   sql.NullInt64{Int64: runID, Valid: true},
		TaskID:  "CW-20260510-0113",
		Type:    "progress",
		Payload: `{"value":0.5}`,
	}))
	require.NoError(t, writer.RecordCost(context.Background(), &sqlstore.CostLedgerRecord{
		TaskID:           "CW-20260510-0113",
		RunID:            runID,
		Cost:             0.42,
		PromptTokens:     12,
		CompletionTokens: 34,
		CostSource:       "executor",
	}))

	require.Eventually(t, func() bool {
		events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-20260510-0113"})
		if err != nil || len(events) != 1 {
			return false
		}
		var count int
		err = store.ReadDB().QueryRow(`SELECT COUNT(*) FROM cost_ledger WHERE task_id = ?`, "CW-20260510-0113").Scan(&count)
		return err == nil && count == 1
	}, 3*time.Second, 25*time.Millisecond)
}

func TestWriter_AddCommentWaitsForPersistence(t *testing.T) {
	store, writer, _ := setupWriter(t, writequeue.DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = writer.Start(ctx) }()

	rec := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   "CW-20260510-0113",
		Author:     "user",
		Content:    "queued comment",
	}
	require.NoError(t, writer.AddComment(context.Background(), rec))
	require.NotZero(t, rec.ID)
	require.False(t, rec.CreatedAt.IsZero())

	comments, err := store.ListComments("CW-20260510-0113")
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "queued comment", comments[0].Content)
}

func TestWriter_ReclaimsReservedJobsAfterCrash(t *testing.T) {
	cfg := writequeue.DefaultConfig()
	cfg.RetryAfter = 50 * time.Millisecond
	cfg.PollInterval = 10 * time.Millisecond

	store, writer, queueDB := setupWriter(t, cfg)

	require.NoError(t, writer.AppendRunEvent(context.Background(), &sqlstore.RunEventRecord{
		TaskID:  "CW-20260510-0113",
		Type:    "log",
		Payload: `{"line":"after restart"}`,
	}))

	_, err := queueDB.Exec(
		`UPDATE `+cfg.JobsTable+` SET reserved_at = ?, attempts = 1 WHERE type = ?`,
		time.Now().Add(-time.Second).Unix(),
		"run_event",
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = writer.Start(ctx) }()

	require.Eventually(t, func() bool {
		events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-20260510-0113"})
		return err == nil && len(events) == 1
	}, 3*time.Second, 25*time.Millisecond)
}
