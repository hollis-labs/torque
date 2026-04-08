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
}
