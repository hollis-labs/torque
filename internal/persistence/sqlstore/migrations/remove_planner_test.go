package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

func TestRemovePlannerPreservesTaskData(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "retirement.db"))
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "045_task_id_counters.sql")

	_, err = db.Exec(`
INSERT INTO planner_workstreams (id,title) VALUES ('WS-retired','Moved to Tachyon');
INSERT INTO planner_comments (db,item_id,text) VALUES ('workstreams','WS-retired','Planner comment');
INSERT INTO planner_links (db,item_id,kind,ref) VALUES ('workstreams','WS-retired','torque','CW-20261020-0001');
INSERT INTO settings (key,value) VALUES ('features.planner','true'), ('features.projects','true');
INSERT INTO tasks (id,title,kind) VALUES ('CW-20261020-0001','Keep execution plan','plan');
INSERT INTO judgments (subject_type,subject_id,set_id,set_version,set_hash,question_key,answer_type,state_hash)
VALUES ('planner:workstreams','WS-retired','test',1,'hash','question','choice','state'),
       ('task','CW-20261020-0001','test',1,'hash','question','choice','state');
`)
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db))

	for _, table := range []string{
		"planner_priorities", "planner_workstreams", "planner_roadmap",
		"planner_ideas", "planner_decisions", "planner_risks", "planner_inbox",
		"planner_comments", "planner_links",
	} {
		_, err := db.Exec("SELECT * FROM " + table)
		require.ErrorContains(t, err, "no such table")
	}
	var value string
	require.ErrorIs(t, db.QueryRow("SELECT value FROM settings WHERE key = 'features.planner'").Scan(&value), sql.ErrNoRows)
	require.NoError(t, db.QueryRow("SELECT value FROM settings WHERE key = 'features.projects'").Scan(&value))
	require.Equal(t, "true", value)
	require.NoError(t, db.QueryRow("SELECT title FROM tasks WHERE id = 'CW-20261020-0001' AND kind = 'plan'").Scan(&value))
	require.Equal(t, "Keep execution plan", value)
	require.NoError(t, db.QueryRow("SELECT subject_id FROM judgments WHERE subject_type = 'task'").Scan(&value))
	require.Equal(t, "CW-20261020-0001", value)
	require.ErrorIs(t, db.QueryRow("SELECT subject_id FROM judgments WHERE subject_type LIKE 'planner:%'").Scan(&value), sql.ErrNoRows)
}
