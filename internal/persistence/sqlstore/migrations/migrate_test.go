package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrationsApply(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = migrations.Run(db)
	require.NoError(t, err)

	// Verify 002 columns exist
	_, err = db.Exec("SELECT retry_count, last_run_id, escalation_step FROM tasks LIMIT 0")
	require.NoError(t, err)

	// Verify 002 tables exist
	_, err = db.Exec("SELECT worker_id, task_id, run_id, last_heartbeat FROM worker_heartbeats LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT id, task_id, run_id, cost, prompt_tokens, completion_tokens FROM cost_ledger LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT id, task_id, run_id, queue_job_id, status FROM scheduler_jobs LIMIT 0")
	require.NoError(t, err)

	// Verify 005 tables exist with expected columns
	_, err = db.Exec("SELECT slug, name, description, color, created_at, updated_at FROM tags LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT task_id, tag_slug, sort_order, created_at FROM task_tags LIMIT 0")
	require.NoError(t, err)

	// Verify 005 dropped the legacy tasks.tags column
	_, err = db.Exec("SELECT tags FROM tasks LIMIT 0")
	require.Error(t, err, "tasks.tags column should have been dropped by migration 005")

	// Verify 006 made run_events.run_id nullable: inserting with NULL should succeed.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('T1', 'test', 'todo')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_events (run_id, task_id, type, payload) VALUES (NULL, 'T1', 'task_transitioned', '{}')`)
	require.NoError(t, err, "run_events.run_id should be nullable after migration 006")

	// Verify 007 added facet columns to tasks.
	_, err = db.Exec(`SELECT kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response FROM tasks LIMIT 0`)
	require.NoError(t, err, "tasks facet columns should exist after migration 007")

	// Verify 007 enforces kind CHECK constraint.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T7bad', 'bad', 'todo', 'nonsense')`)
	require.Error(t, err, "tasks.kind CHECK should reject unknown kinds")

	// Verify 007 allows valid kinds with defaults for other facet columns.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T7ok', 'ok', 'todo', 'wait')`)
	require.NoError(t, err, "tasks.kind should accept 'wait'")

	// Verify 007 created the new indexes.
	idxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='tasks'`)
	require.NoError(t, err)
	defer idxRows.Close()
	idx := map[string]bool{}
	for idxRows.Next() {
		var n string
		require.NoError(t, idxRows.Scan(&n))
		idx[n] = true
	}
	require.True(t, idx["idx_tasks_kind_status"], "idx_tasks_kind_status should exist")
	require.True(t, idx["idx_tasks_source"], "idx_tasks_source should exist")
	require.True(t, idx["idx_tasks_checkpoint_mode"], "idx_tasks_checkpoint_mode should exist")
}
