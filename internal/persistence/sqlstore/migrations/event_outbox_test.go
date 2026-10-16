package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

// 042 creates the outbox and seeds exactly one meta row; re-running changes
// nothing, and a hand-bumped generation survives a re-run.
func TestMigration042EventOutboxSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "outbox.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrations.Run(db))

	var n, gen, pruned int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*), MIN(generation), MIN(pruned_through) FROM event_outbox_meta`).Scan(&n, &gen, &pruned))
	require.Equal(t, 1, n)
	require.Equal(t, 1, gen)
	require.Equal(t, 0, pruned)

	_, err = db.Exec(`UPDATE event_outbox_meta SET generation = 2`)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*), MIN(generation) FROM event_outbox_meta`).Scan(&n, &gen))
	require.Equal(t, 1, n)
	require.Equal(t, 2, gen)

	// event_id is unique: a retried append with the same id cannot double-insert.
	_, err = db.Exec(`INSERT INTO event_outbox (event_id, kind, occurred_at, entity_type, entity_id, trigger_kind, envelope)
		VALUES ('evt_a','task.state_changed','t','task','T','system','{}')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO event_outbox (event_id, kind, occurred_at, entity_type, entity_id, trigger_kind, envelope)
		VALUES ('evt_a','task.state_changed','t','task','T','system','{}')`)
	require.Error(t, err)
}
