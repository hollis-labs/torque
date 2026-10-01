package migrations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration035PopulatedSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "populated.db"))
	require.NoError(t, err)
	defer db.Close()
	applyMigrationFilesThroughAndMarkApplied(t, db, "034_run_cost_provenance.sql")
	_, err = db.Exec(`INSERT INTO projects (id,name) VALUES ('P35','project');
 INSERT INTO epics (id,name,project_id) VALUES ('E35','epic','P35');
 INSERT INTO sprints (id,name,project_id) VALUES ('SP35','sprint','P35');
 INSERT INTO tasks (id,title,project_id,epic_id,sprint_id) VALUES ('T35','before 035','P35','E35','SP35');
 INSERT INTO runs (id,task_id,started_at,ended_at,cost) VALUES (35,'T35','2026-09-01 01:02:03.123456789 +0000 UTC','2026-09-01 01:02:04.123456789 +0000 UTC',1.25);
 INSERT INTO cost_ledger (task_id,run_id,cost) VALUES ('T35',35,1),('T35',35,0.25);
 INSERT INTO artifacts (task_id,type,content) VALUES ('T35','log','evidence');
 INSERT INTO comments (entity_type,entity_id,content) VALUES ('task','T35','comment');
 INSERT INTO sessions (id,task_id,project_id,meta) VALUES ('S35','T35','P35','{"before":true}');
 INSERT INTO messages (id,kind,from_kind,from_authority,from_id,from_urn,to_kind,to_authority,to_id,to_urn,created_at)
 VALUES ('M35','notice','agent','torque','a','msg://agent/torque/a','agent','torque','b','msg://agent/torque/b','2026-09-01 01:02:03');
 INSERT INTO collections (id,name) VALUES ('COL35','collection');
 INSERT INTO checkpoints (task_id,correlation_id,type,payload_json,emitter_source_type) VALUES ('T35','CP35','message','{}','agent');
 INSERT INTO task_templates (id,version,name,description,kind) VALUES ('TPL35',1,'template','description','agent');
 PRAGMA foreign_keys=ON;`)
	require.NoError(t, err)
	verify035Upgrade(t, db, "?")
	assertNoForeignKeyViolations(t, db)
	var enabled int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled))
	require.Equal(t, 1, enabled)
	var started, ended string
	require.NoError(t, db.QueryRow(`SELECT CAST(started_at AS TEXT), CAST(ended_at AS TEXT) FROM runs WHERE id=35`).Scan(&started, &ended))
	require.Equal(t, "2026-09-01 01:02:03.123456789 +0000 UTC", started)
	require.Equal(t, "2026-09-01 01:02:04.123456789 +0000 UTC", ended)
}

// An explicit disposable PostgreSQL DSN is required. Every invocation creates
// and drops its own schema; it never uses Torque's configured database path/DSN.
// This is an upgrade fixture, not a claim that pre-035 bootstrap SQL is portable.
func TestMigration035PopulatedPostgres(t *testing.T) {
	db := postgresMigrationFixture(t)
	_, err := db.Exec(`
 CREATE TABLE projects (id TEXT PRIMARY KEY,name TEXT,status TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,archived_at TIMESTAMPTZ);
 CREATE TABLE epics (id TEXT PRIMARY KEY,name TEXT,status TEXT,project_id TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,archived_at TIMESTAMPTZ);
 CREATE TABLE sprints (id TEXT PRIMARY KEY,name TEXT,status TEXT,project_id TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,archived_at TIMESTAMPTZ);
 CREATE TABLE tasks (id TEXT PRIMARY KEY,title TEXT,status TEXT,priority INTEGER,kind TEXT,project_id TEXT REFERENCES projects(id),epic_id TEXT REFERENCES epics(id),sprint_id TEXT REFERENCES sprints(id),parent_id TEXT REFERENCES tasks(id),collection_id TEXT,collection_position INTEGER,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ);
 CREATE TABLE runs (id BIGINT PRIMARY KEY,task_id TEXT REFERENCES tasks(id),status TEXT,started_at TIMESTAMPTZ,ended_at TIMESTAMPTZ,cost REAL);
 CREATE TABLE cost_ledger (id BIGSERIAL PRIMARY KEY,task_id TEXT REFERENCES tasks(id),run_id BIGINT REFERENCES runs(id),cost REAL);
 CREATE TABLE artifacts (id BIGINT PRIMARY KEY,task_id TEXT REFERENCES tasks(id),type TEXT,content TEXT,created_at TIMESTAMPTZ);
 CREATE TABLE comments (id BIGINT PRIMARY KEY,entity_type TEXT,entity_id TEXT,content TEXT,created_at TIMESTAMPTZ);
 CREATE TABLE sessions (id TEXT PRIMARY KEY,state TEXT,task_id TEXT,project_id TEXT,meta TEXT,created_at TIMESTAMPTZ);
 CREATE TABLE messages (id TEXT PRIMARY KEY,to_urn TEXT,thread_id TEXT,created_at TIMESTAMPTZ);
 CREATE TABLE collections (id TEXT PRIMARY KEY,name TEXT,created_at TIMESTAMPTZ);
 CREATE TABLE checkpoints (id BIGINT PRIMARY KEY,task_id TEXT REFERENCES tasks(id),status TEXT,emitted_at TIMESTAMPTZ);
 CREATE TABLE task_templates (id TEXT,version INTEGER,is_archived INTEGER,PRIMARY KEY(id,version));
 CREATE TABLE schema_migrations (version TEXT PRIMARY KEY,applied_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP);
 INSERT INTO projects (id,name) VALUES ('P35','project');
 INSERT INTO epics (id,name,project_id) VALUES ('E35','epic','P35');
 INSERT INTO sprints (id,name,project_id) VALUES ('SP35','sprint','P35');
 INSERT INTO tasks (id,title,project_id,epic_id,sprint_id) VALUES ('T35','before 035','P35','E35','SP35');
 INSERT INTO runs (id,task_id,started_at,ended_at,cost) VALUES (35,'T35','2026-09-01 01:02:03.123456+00','2026-09-01 01:02:04.123456+00',1.25);
 INSERT INTO cost_ledger (task_id,run_id,cost) VALUES ('T35',35,1),('T35',35,0.25);
 INSERT INTO artifacts (id,task_id,type,content) VALUES (35,'T35','log','evidence');
 INSERT INTO comments (id,entity_type,entity_id,content) VALUES (35,'task','T35','comment');
 INSERT INTO sessions (id,task_id,project_id,meta) VALUES ('S35','T35','P35','{"before":true}');
 INSERT INTO messages (id,to_urn,created_at) VALUES ('M35','msg://agent/torque/b','2026-09-01 01:02:03+00');
 INSERT INTO collections (id,name) VALUES ('COL35','collection');
 INSERT INTO checkpoints (id,task_id,status) VALUES (35,'T35','pending');
 INSERT INTO task_templates (id,version,is_archived) VALUES ('TPL35',1,0);`)
	require.NoError(t, err)
	files, err := filepath.Glob("*.sql")
	require.NoError(t, err)
	for _, f := range files {
		if f > "034_run_cost_provenance.sql" {
			continue
		}
		_, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES ($1)`, f)
		require.NoError(t, err)
	}
	verify035Upgrade(t, db, "$1")
	// Migration must retain FK enforcement and existing links.
	_, err = db.Exec(`INSERT INTO runs (id,task_id) VALUES (36,'missing')`)
	require.Error(t, err)
	var duration int64
	require.NoError(t, db.QueryRow(`SELECT CAST(ROUND(EXTRACT(EPOCH FROM (ended_at-started_at))*1000) AS BIGINT) FROM runs WHERE id=35`).Scan(&duration))
	require.Equal(t, int64(1000), duration)
}

func verify035Upgrade(t *testing.T, db *sql.DB, placeholder string) {
	t.Helper()
	require.NoError(t, migrations.Run(db))
	require.NoError(t, migrations.Run(db), "second run should leave existing rows intact")
	var title, content, meta string
	var cost float64
	require.NoError(t, db.QueryRow(`SELECT title FROM tasks WHERE id='T35'`).Scan(&title))
	require.Equal(t, "before 035", title)
	require.NoError(t, db.QueryRow(`SELECT cost FROM runs WHERE id=35`).Scan(&cost))
	require.Equal(t, 1.25, cost)
	require.NoError(t, db.QueryRow(`SELECT SUM(cost) FROM cost_ledger WHERE run_id=35`).Scan(&cost))
	require.Equal(t, 1.25, cost, "multiple ledger rows per run must be preserved")
	require.NoError(t, db.QueryRow(`SELECT content FROM artifacts WHERE task_id='T35'`).Scan(&content))
	require.Equal(t, "evidence", content)
	require.NoError(t, db.QueryRow(`SELECT content FROM comments WHERE entity_id='T35'`).Scan(&content))
	require.Equal(t, "comment", content)
	for table, id := range map[string]string{"projects": "P35", "epics": "E35", "sprints": "SP35", "messages": "M35", "collections": "COL35", "task_templates": "TPL35"} {
		var got string
		require.NoError(t, db.QueryRow(`SELECT id FROM `+table+` WHERE id=`+placeholder, id).Scan(&got))
		require.Equal(t, id, got)
	}
	require.NoError(t, db.QueryRow(`SELECT meta FROM sessions WHERE id='S35'`).Scan(&meta))
	require.JSONEq(t, `{"before":true}`, meta)
	var version string
	require.NoError(t, db.QueryRow(`SELECT version FROM schema_migrations WHERE version = `+placeholder, "035_list_sort_indexes.sql").Scan(&version))
	require.Equal(t, "035_list_sort_indexes.sql", version)
	// Exercise indexes as the database's objects, rather than asserting a mutable
	// index count or an optimizer's choice on a tiny test fixture.
	_, err := db.Exec(`UPDATE tasks SET priority=1 WHERE id='T35'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE runs SET cost=2.5 WHERE id=35`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT cost FROM runs ORDER BY COALESCE(cost,0) DESC,id ASC LIMIT 1`).Scan(&cost))
	require.Equal(t, 2.5, cost)
}

func TestMigration035PostgresFailureRollsBack(t *testing.T) {
	db := postgresMigrationFixture(t)
	_, err := db.Exec(`CREATE TABLE tasks (id TEXT PRIMARY KEY, priority INTEGER, status TEXT, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ);
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO tasks (id,priority) VALUES ('T35',2);`)
	require.NoError(t, err)
	files, err := filepath.Glob("*.sql")
	require.NoError(t, err)
	for _, f := range files {
		if f > "034_run_cost_provenance.sql" {
			continue
		}
		_, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES ($1)`, f)
		require.NoError(t, err)
	}
	// 035 creates task indexes, then fails because this fixture deliberately
	// lacks projects. Neither those indexes nor its version may be committed.
	require.ErrorContains(t, migrations.Run(db), "execute migration 035_list_sort_indexes.sql")
	var exists bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_tasks_page_priority_asc')`).Scan(&exists))
	require.False(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='035_list_sort_indexes.sql')`).Scan(&exists))
	require.False(t, exists)
	var priority int
	require.NoError(t, db.QueryRow(`SELECT priority FROM tasks WHERE id='T35'`).Scan(&priority))
	require.Equal(t, 2, priority)
}
