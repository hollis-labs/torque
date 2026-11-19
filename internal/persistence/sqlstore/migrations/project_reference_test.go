package migrations_test

import (
	"database/sql"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestMigration048ProjectExternalReference(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "references.db"))
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "047_project_execution_defaults.sql")
	_, err = db.Exec(`INSERT INTO projects (id,name,repo_path) VALUES ('legacy-1','same','/repo'),('legacy-2','same','/repo')`)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db))
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM projects WHERE external_authority IS NULL AND external_id IS NULL`).Scan(&n))
	require.Equal(t, 2, n)
	_, err = db.Exec(`INSERT INTO projects (id,name,external_authority,external_id) VALUES ('ref','same','registry','project:tether')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO projects (id,name,external_authority,external_id) VALUES ('dup','same','registry','project:tether')`)
	require.Error(t, err)
	for _, values := range []string{`'registry',NULL`, `NULL,'id'`, `'','id'`, `'registry',''`, `' ','id'`} {
		_, err = db.Exec(`INSERT INTO projects (id,name,external_authority,external_id) VALUES ('bad','same',` + values + `)`)
		require.Error(t, err)
	}
	_, err = db.Exec(`UPDATE projects SET external_id=NULL WHERE id='ref'`)
	require.Error(t, err)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version='048_project_external_reference.sql'`).Scan(&n))
	require.Equal(t, 1, n)
}
