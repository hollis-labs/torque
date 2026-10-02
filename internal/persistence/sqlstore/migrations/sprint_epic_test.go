package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration036PopulatedSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sprints.db"))
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "035_list_sort_indexes.sql")
	_, err = db.Exec(`PRAGMA foreign_keys=ON;
 INSERT INTO projects(id,name) VALUES ('P36','project');
 INSERT INTO epics(id,name,project_id) VALUES ('E36','epic','P36');
 INSERT INTO sprints(id,name,goal,status,approval_mode,project_id,cost_budget) VALUES ('SP36','kept','goal','inactive','auto','P36',42);
 INSERT INTO tasks(id,title,sprint_id) VALUES ('T36','kept task','SP36');`)
	require.NoError(t, err)
	verifySprintEpicUpgrade(t, db)
	assertNoForeignKeyViolations(t, db)
	var plan string
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id FROM sprints WHERE epic_id='E36'`)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var id, parent, unused int
	require.NoError(t, rows.Scan(&id, &parent, &unused, &plan))
	t.Log("epic filter plan:", plan)
}

func TestMigration036PopulatedPostgres(t *testing.T) {
	db := postgresMigrationFixture(t)
	_, err := db.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE epics(id TEXT PRIMARY KEY);
 CREATE TABLE sprints(id TEXT PRIMARY KEY,name TEXT,goal TEXT,status TEXT,approval_mode TEXT,project_id TEXT,cost_budget REAL);
 CREATE TABLE tasks(id TEXT PRIMARY KEY,title TEXT,sprint_id TEXT REFERENCES sprints(id));
 INSERT INTO epics(id) VALUES ('E36');
 INSERT INTO sprints VALUES ('SP36','kept','goal','inactive','auto','P36',42);
 INSERT INTO tasks VALUES ('T36','kept task','SP36');`)
	require.NoError(t, err)
	files, err := filepath.Glob("*.sql")
	require.NoError(t, err)
	for _, f := range files {
		if f <= "035_list_sort_indexes.sql" {
			_, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES ($1)`, f)
			require.NoError(t, err)
		}
	}
	verifySprintEpicUpgrade(t, db)
}

func verifySprintEpicUpgrade(t *testing.T, db *sql.DB) {
	t.Helper()
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db))
	var name, goal, status, approval, project string
	var budget float64
	var epic sql.NullString
	require.NoError(t, db.QueryRow(`SELECT name,goal,status,approval_mode,project_id,cost_budget,epic_id FROM sprints WHERE id='SP36'`).Scan(&name, &goal, &status, &approval, &project, &budget, &epic))
	require.Equal(t, []string{"kept", "goal", "inactive", "auto", "P36"}, []string{name, goal, status, approval, project})
	require.Equal(t, float64(42), budget)
	require.False(t, epic.Valid)
	var title, sprint string
	require.NoError(t, db.QueryRow(`SELECT title,sprint_id FROM tasks WHERE id='T36'`).Scan(&title, &sprint))
	require.Equal(t, "kept task", title)
	require.Equal(t, "SP36", sprint)
	_, err := db.Exec(`UPDATE sprints SET epic_id='missing' WHERE id='SP36'`)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE sprints SET epic_id='E36' WHERE id='SP36'`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM epics WHERE id='E36'`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT epic_id FROM sprints WHERE id='SP36'`).Scan(&epic))
	require.False(t, epic.Valid, "epic deletion must preserve sprint and clear link")
}
