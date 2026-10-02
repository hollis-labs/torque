package migrations_test

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration037PopulatedSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	applyMigrationFilesThroughAndMarkApplied(t, db, "034_run_cost_provenance.sql")
	_, err = db.Exec(`INSERT INTO tasks(id,title) VALUES ('T37','duration upgrade');
 INSERT INTO runs(id,task_id,started_at,ended_at,cost) VALUES
 (1,'T37','2026-10-01 00:00:00',NULL,1),
 (2,'T37','2026-10-01 00:00:00','2026-10-01 00:00:01',2),
 (3,'T37','2026-10-01 00:00:00','2026-10-01 00:00:01',3),
 (4,'T37','2026-10-01 00:00:00','2026-10-01 00:00:02',4),
 (5,'T37','2020-01-01T00:00:00.608407Z','2020-01-01T00:00:24.999907Z',5)`)
	require.NoError(t, err)
	verify037Upgrade(t, db, false)
	var integrity string
	require.NoError(t, db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	require.Equal(t, "ok", integrity)
	assertNoForeignKeyViolations(t, db)
	// Also inspect with the system SQLite build when available: this timestamp
	// fixture reproduces different julianday results on 3.53.4 versus 3.46.1.
	if cli, err := exec.LookPath("sqlite3"); err == nil {
		out, err := exec.Command(cli, path, "PRAGMA integrity_check;").CombinedOutput()
		require.NoError(t, err, string(out))
		require.Equal(t, "ok", strings.TrimSpace(string(out)))
	}
}

func TestMigration037PopulatedPostgres(t *testing.T) {
	db := populatedPostgresThrough034(t)
	verify037Upgrade(t, db, true)
}

func verify037Upgrade(t *testing.T, db *sql.DB, postgres bool) {
	t.Helper()
	file, placeholder := "035_list_sort_indexes.sql", "?"
	catalog := `SELECT name FROM sqlite_master WHERE type='index'`
	duration := `COALESCE(CAST(ROUND((julianday(ended_at)-julianday(started_at))*86400000) AS INTEGER), -1)`
	if postgres {
		file, placeholder = "postgres/"+file, "$1"
		catalog = `SELECT indexname FROM pg_indexes WHERE schemaname=current_schema()`
		duration = `COALESCE(CAST(ROUND(EXTRACT(EPOCH FROM (ended_at-started_at))*1000) AS BIGINT), -1)`
	}
	// Apply the actual 035 DDL to populated pre-035 tables, then let the runner
	// apply 037. Catalog comparisons inspect database objects, not source text.
	applyMigrationFile(t, db, file)
	_, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES (`+placeholder+`)`, "035_list_sort_indexes.sql")
	require.NoError(t, err)
	before := indexCatalog(t, db, catalog)
	for _, name := range []string{"idx_runs_page_duration_asc", "idx_runs_page_duration_desc"} {
		require.Contains(t, before, name)
	}
	orders := map[string][]int64{}
	for _, dir := range []string{"ASC", "DESC"} {
		orders[dir] = durationOrder(t, db, duration, dir)
	}
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db), "upgrade is idempotent")
	after := indexCatalog(t, db, catalog)
	require.NotContains(t, after, "idx_runs_page_duration_asc")
	require.NotContains(t, after, "idx_runs_page_duration_desc")
	for name := range before {
		if name != "idx_runs_page_duration_asc" && name != "idx_runs_page_duration_desc" {
			require.Contains(t, after, name, "every other index, including all other 035 indexes, must remain")
		}
	}
	for _, dir := range []string{"ASC", "DESC"} {
		require.Equal(t, orders[dir], durationOrder(t, db, duration, dir))
	}
	var totalCost float64
	require.NoError(t, db.QueryRow(`SELECT SUM(cost) FROM runs`).Scan(&totalCost))
	if !postgres {
		require.Equal(t, 15.0, totalCost, "run rows are preserved")
		require.Equal(t, []int64{1, 2, 3, 4, 5}, orders["ASC"])
		require.Equal(t, []int64{5, 4, 2, 3, 1}, orders["DESC"])
	}
	var version string
	require.NoError(t, db.QueryRow(`SELECT version FROM schema_migrations WHERE version=`+placeholder, "037_drop_duration_indexes.sql").Scan(&version))
	require.Equal(t, "037_drop_duration_indexes.sql", version)
}

func indexCatalog(t *testing.T, db *sql.DB, query string) map[string]bool {
	t.Helper()
	rows, err := db.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names[name] = true
	}
	require.NoError(t, rows.Err())
	return names
}

func durationOrder(t *testing.T, db *sql.DB, expression, direction string) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM runs ORDER BY ` + expression + ` ` + direction + `,id ASC`)
	require.NoError(t, err)
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}
