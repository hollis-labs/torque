package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func verifyCapabilityProfileUpgrade(t *testing.T, db *sql.DB) {
	t.Helper()
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db))

	var role, tier string
	var capProfile sql.NullString
	err := db.QueryRow(`SELECT role, tier, capability_profile FROM tasks WHERE id = 'T1'`).Scan(&role, &tier, &capProfile)
	require.NoError(t, err)
	require.Equal(t, "", role)
	require.Equal(t, "", tier)
	require.False(t, capProfile.Valid)

	err = db.QueryRow(`SELECT role, tier, capability_profile FROM sessions WHERE id = 'S1'`).Scan(&role, &tier, &capProfile)
	require.NoError(t, err)
	require.Equal(t, "", role)
	require.Equal(t, "", tier)
	require.False(t, capProfile.Valid)

	var runsCap sql.NullString
	err = db.QueryRow(`SELECT profile_snapshot FROM runs WHERE id = 1`).Scan(&runsCap)
	require.NoError(t, err)
	require.False(t, runsCap.Valid)

	var tplRole sql.NullString
	err = db.QueryRow(`SELECT role, tier, capability_profile FROM task_templates WHERE id = 'TT1'`).Scan(&tplRole, &tplRole, &capProfile)
	require.NoError(t, err)
	require.False(t, capProfile.Valid)
}

func markAppliedThrough(t *testing.T, db *sql.DB, target string) {
	files, err := filepath.Glob("*.sql")
	require.NoError(t, err)
	for _, f := range files {
		if f <= target {
			_, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES ($1)`, f)
			require.NoError(t, err)
		}
	}
}

func TestMigration038PopulatedPostgres(t *testing.T) {
	db := postgresMigrationFixture(t)
	_, err := db.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE tasks(id TEXT PRIMARY KEY);
 CREATE TABLE task_templates(id TEXT PRIMARY KEY, version INTEGER);
 CREATE TABLE sessions(id TEXT PRIMARY KEY);
 CREATE TABLE runs(id SERIAL PRIMARY KEY);
 INSERT INTO tasks(id) VALUES ('T1');
 INSERT INTO task_templates(id, version) VALUES ('TT1', 1);
 INSERT INTO sessions(id) VALUES ('S1');
 INSERT INTO runs(id) VALUES (1);`)
	require.NoError(t, err)

	markAppliedThrough(t, db, "037_drop_duration_indexes.sql")
	verifyCapabilityProfileUpgrade(t, db)
}

func TestMigration038PopulatedSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cap.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE tasks(id TEXT PRIMARY KEY);
 CREATE TABLE task_templates(id TEXT PRIMARY KEY, version INTEGER);
 CREATE TABLE sessions(id TEXT PRIMARY KEY);
 CREATE TABLE runs(id INTEGER PRIMARY KEY);
 INSERT INTO tasks(id) VALUES ('T1');
 INSERT INTO task_templates(id, version) VALUES ('TT1', 1);
 INSERT INTO sessions(id) VALUES ('S1');
 INSERT INTO runs(id) VALUES (1);`)
	require.NoError(t, err)

	markAppliedThrough(t, db, "037_drop_duration_indexes.sql")
	verifyCapabilityProfileUpgrade(t, db)
}
