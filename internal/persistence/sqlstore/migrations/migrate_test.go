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
}
