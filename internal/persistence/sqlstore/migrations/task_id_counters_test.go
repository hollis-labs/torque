package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

// 045 seeds the per-day counter from every task id that exists and from every
// task id the outbox has recorded (including tasks that are gone), using the
// numeric suffix so 5-digit and longer ids count.
func TestMigration045SeedsCountersFromTasksAndOutbox(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ids.db"))
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "044_self_report.sql")

	_, err = db.Exec(`
INSERT INTO tasks (id,title) VALUES
  ('CW-20261001-0003','a'),
  ('CW-20261001-0007','b'),
  ('CW-20261002-9999','c'),
  ('CW-20261002-10000','d'),
  ('NOT-A-CW-ID','e');
-- a task that existed, was deleted, and has outbox history only
INSERT INTO event_outbox (event_id,kind,occurred_at,entity_type,entity_id,task_id,trigger_kind,envelope)
VALUES ('evt_1','task.state_changed','t','task','CW-20261001-0012','CW-20261001-0012','agent','{}'),
       ('evt_2','task.state_changed','t','task','CW-20261003-0002','CW-20261003-0002','agent','{}'),
       ('evt_3','checkpoint.state_changed','t','checkpoint','C1',NULL,'agent','{}');`)
	require.NoError(t, err)

	require.NoError(t, migrations.Run(db))

	got := map[string]int{}
	rows, err := db.Query(`SELECT day, last_seq FROM task_id_counters`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var d string
		var n int
		require.NoError(t, rows.Scan(&d, &n))
		got[d] = n
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]int{
		"20261001": 12,    // the deleted task's outbox id (12) beats the live max (7)
		"20261002": 10000, // numeric, not lexicographic: 10000 > 9999
		"20261003": 2,     // a day that has only outbox history
	}, got)
}

// A database with no tasks or events gets an empty counter table, and re-running
// the migrator is a no-op.
func TestMigration045EmptyDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db))
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM task_id_counters`).Scan(&n))
	require.Zero(t, n)
}
