package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration044BackfillsSurvivingRunOrdinals(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "041_judgments.sql")
	_, err = db.Exec(`INSERT INTO tasks(id,title) VALUES ('A','a'),('B','b');
        INSERT INTO runs(id,task_id,executor,status) VALUES
        (2,'A','cli','done'),(7,'B','cli','blocked'),(9,'A','cli','cancelled'),(20,'A','cli','running')`)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	for id, expected := range map[int]int{2: 1, 7: 1, 9: 2, 20: 3} {
		var attempt int
		require.NoError(t, db.QueryRow("SELECT attempt FROM runs WHERE id = ?", id).Scan(&attempt))
		require.Equal(t, expected, attempt)
	}
	var counter int
	require.NoError(t, db.QueryRow("SELECT last_attempt FROM task_run_counters WHERE task_id='A'").Scan(&counter))
	require.Equal(t, 3, counter)
	// An older binary can keep writing after an additive migration.
	_, err = db.Exec("INSERT INTO runs(task_id,executor) VALUES ('A','cli'),('A','cli')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO runs(task_id,executor,attempt) VALUES ('A','cli',3)")
	require.Error(t, err, "an assigned attempt is unique within its task")
	require.NoError(t, migrations.Run(db), "a restart must not renumber history")
	var value, source sql.NullString
	require.NoError(t, db.QueryRow("SELECT progress_detail,progress_detail_source FROM tasks WHERE id='A'").Scan(&value, &source))
	require.False(t, value.Valid)
	require.False(t, source.Valid)
}
