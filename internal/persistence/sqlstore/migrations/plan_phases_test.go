package migrations_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

// uninstall043 returns a database to its pre-043 shape so the backfill can be
// run over legacy rows.
func uninstall043(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`DROP TRIGGER trg_tasks_phase_insert`,
		`DROP TRIGGER trg_tasks_phase_update`,
		`DROP TRIGGER trg_tasks_plan_delete`,
		`DROP INDEX idx_tasks_parent_phase`,
		`DROP TABLE plan_phase_repairs`,
		`DROP TABLE plan_phases`,
		`ALTER TABLE tasks DROP COLUMN phase_id`,
		`DELETE FROM schema_migrations WHERE version = '043_plan_phases.sql'`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err, stmt)
	}
}

func insertTask(t *testing.T, db *sql.DB, id, kind, status, parent, metadata string) {
	t.Helper()
	var p, m any
	if parent != "" {
		p = parent
	}
	if metadata != "" {
		m = metadata
	}
	_, err := db.Exec(`INSERT INTO tasks (id, title, kind, status, parent_id, metadata) VALUES (?, ?, ?, ?, ?, ?)`,
		id, id, kind, status, p, m)
	require.NoError(t, err, id)
}

func TestMigration043PlanPhasesBackfillSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "phases.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrations.Run(db))
	uninstall043(t, db)

	insertTask(t, db, "P1", "plan", "todo", "",
		`{"plan":{"version":1,"phases":[{"id":"ph-1","name":"Build","order":1,"acceptance":"works"},{"id":"ph-2","name":"Ship","order":2},{"id":"ph-1","name":"dup","order":9},{"name":"no id"}]}}`)
	insertTask(t, db, "P2", "plan", "todo", "", `not json`)
	insertTask(t, db, "P3", "plan", "todo", "", "")
	// ph-1: all children done -> completed. ph-2: one doing -> started.
	insertTask(t, db, "C1", "agent", "done", "P1", `{"phase_id":"ph-1"}`)
	insertTask(t, db, "C2", "agent", "done", "P1", `{"phase_id":"ph-1"}`)
	insertTask(t, db, "C3", "agent", "doing", "P1", `{"phase_id":"ph-2"}`)
	insertTask(t, db, "C4", "agent", "todo", "P1", `{"phase_id":"ph-2"}`)
	// Unknown phase, no parent, bad metadata: flagged, not invented.
	insertTask(t, db, "C5", "agent", "todo", "P1", `{"phase_id":"ph-9"}`)
	insertTask(t, db, "C6", "agent", "todo", "", `{"phase_id":"ph-1"}`)
	insertTask(t, db, "C7", "agent", "todo", "P1", `{{`)

	require.NoError(t, migrations.Run(db))

	type phase struct {
		name, acceptance, state string
		order, gen              int
		started, completed      sql.NullString
	}
	got := map[string]phase{}
	rows, err := db.Query(`SELECT plan_id || '/' || phase_id, name, acceptance, state, display_order, lifecycle_generation, started_at, completed_at FROM plan_phases`)
	require.NoError(t, err)
	for rows.Next() {
		var k string
		var p phase
		require.NoError(t, rows.Scan(&k, &p.name, &p.acceptance, &p.state, &p.order, &p.gen, &p.started, &p.completed))
		got[k] = p
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2, "duplicate and id-less phases are dropped; bad metadata yields none")
	require.Equal(t, "Build", got["P1/ph-1"].name, "first duplicate wins")
	require.Equal(t, "works", got["P1/ph-1"].acceptance)
	require.Equal(t, "completed", got["P1/ph-1"].state)
	require.Equal(t, "started", got["P1/ph-2"].state)
	require.Equal(t, 1, got["P1/ph-1"].gen)
	require.False(t, got["P1/ph-1"].started.Valid, "no history is invented")
	require.False(t, got["P1/ph-1"].completed.Valid)

	phaseOf := func(id string) sql.NullString {
		var s sql.NullString
		require.NoError(t, db.QueryRow(`SELECT phase_id FROM tasks WHERE id = ?`, id).Scan(&s))
		return s
	}
	require.Equal(t, "ph-1", phaseOf("C1").String)
	require.Equal(t, "ph-2", phaseOf("C4").String)
	for _, id := range []string{"C5", "C6", "C7"} {
		require.False(t, phaseOf(id).Valid, id)
	}

	var repairs int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM plan_phase_repairs WHERE task_id IN ('C5','C6')`).Scan(&repairs))
	require.Equal(t, 2, repairs)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM plan_phase_repairs`).Scan(&repairs))
	require.Equal(t, 2, repairs)
}

func TestMigration043PhaseMembershipIsValidatedSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "phases.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrations.Run(db))
	db.SetMaxOpenConns(1) // foreign keys are per connection; the app runs with them on
	_, err = db.Exec(`PRAGMA foreign_keys = ON`)
	require.NoError(t, err)

	insertTask(t, db, "P1", "plan", "todo", "", "")
	insertTask(t, db, "P2", "plan", "todo", "", "")
	_, err = db.Exec(`INSERT INTO plan_phases (plan_id, phase_id, name) VALUES ('P1','ph-1','one'), ('P2','ph-1','other')`)
	require.NoError(t, err)

	// Insert: needs a parent that has the phase.
	_, err = db.Exec(`INSERT INTO tasks (id, title, phase_id) VALUES ('X1','x','ph-1')`)
	require.ErrorContains(t, err, "invalid phase")
	_, err = db.Exec(`INSERT INTO tasks (id, title, parent_id, phase_id) VALUES ('X1','x','P1','ph-7')`)
	require.ErrorContains(t, err, "invalid phase")
	_, err = db.Exec(`INSERT INTO tasks (id, title, parent_id, phase_id) VALUES ('X1','x','P1','ph-1')`)
	require.NoError(t, err)

	// Update: moving a member to another plan, or off its parent, keeps the rule.
	_, err = db.Exec(`UPDATE tasks SET parent_id = NULL WHERE id = 'X1'`)
	require.ErrorContains(t, err, "invalid phase")
	_, err = db.Exec(`UPDATE tasks SET parent_id = 'P2' WHERE id = 'X1'`)
	require.NoError(t, err, "P2 has its own ph-1")
	_, err = db.Exec(`UPDATE tasks SET phase_id = 'ph-3' WHERE id = 'X1'`)
	require.ErrorContains(t, err, "invalid phase")

	// A tombstoned phase takes no new members.
	_, err = db.Exec(`UPDATE plan_phases SET deleted_at = '2026-10-16' WHERE plan_id = 'P2' AND phase_id = 'ph-1'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO tasks (id, title, parent_id, phase_id) VALUES ('X2','x','P2','ph-1')`)
	require.ErrorContains(t, err, "invalid phase")

	// Deleting a plan removes its phases and detaches its members first.
	_, err = db.Exec(`DELETE FROM tasks WHERE id = 'P2'`)
	require.NoError(t, err)
	var phase sql.NullString
	require.NoError(t, db.QueryRow(`SELECT phase_id FROM tasks WHERE id = 'X1'`).Scan(&phase))
	require.False(t, phase.Valid)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM plan_phases WHERE plan_id = 'P2'`).Scan(&n))
	require.Equal(t, 0, n)
}

// The Postgres override is run directly against a minimal tasks table, because
// the historical Postgres bootstrap is not portable (see postgres/README.md).
func TestMigration043PlanPhasesPostgres(t *testing.T) {
	db := postgresMigrationFixture(t)
	_, err := db.Exec(`CREATE TABLE tasks (id TEXT PRIMARY KEY, title TEXT, kind TEXT, status TEXT,
		parent_id TEXT REFERENCES tasks(id) ON DELETE SET NULL, metadata TEXT)`)
	require.NoError(t, err)

	ins := func(id, kind, status, parent, metadata string) {
		var p, m any
		if parent != "" {
			p = parent
		}
		if metadata != "" {
			m = metadata
		}
		_, err := db.Exec(`INSERT INTO tasks (id, title, kind, status, parent_id, metadata) VALUES ($1,$1,$2,$3,$4,$5)`, id, kind, status, p, m)
		require.NoError(t, err, id)
	}
	ins("P1", "plan", "todo", "", `{"plan":{"version":1,"phases":[{"id":"ph-1","name":"Build","order":1,"acceptance":"works"},{"id":"ph-2","name":"Ship","order":2},{"id":"ph-1","name":"dup","order":9},{"name":"no id"}]}}`)
	ins("P2", "plan", "todo", "", `not json`)
	ins("C1", "agent", "done", "P1", `{"phase_id":"ph-1"}`)
	ins("C2", "agent", "done", "P1", `{"phase_id":"ph-1"}`)
	ins("C3", "agent", "doing", "P1", `{"phase_id":"ph-2"}`)
	ins("C4", "agent", "todo", "P1", `{"phase_id":"ph-2"}`)
	ins("C5", "agent", "todo", "P1", `{"phase_id":"ph-9"}`)
	ins("C6", "agent", "todo", "", `{"phase_id":"ph-1"}`)
	ins("C7", "agent", "todo", "P1", `{{`)

	body, err := os.ReadFile("postgres/043_plan_phases.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(body))
	require.NoError(t, err)

	states := map[string]string{}
	rows, err := db.Query(`SELECT plan_id || '/' || phase_id, state FROM plan_phases`)
	require.NoError(t, err)
	for rows.Next() {
		var k, st string
		require.NoError(t, rows.Scan(&k, &st))
		states[k] = st
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]string{"P1/ph-1": "completed", "P1/ph-2": "started"}, states)

	var name string
	require.NoError(t, db.QueryRow(`SELECT name FROM plan_phases WHERE plan_id='P1' AND phase_id='ph-1'`).Scan(&name))
	require.Equal(t, "Build", name)

	var phase sql.NullString
	require.NoError(t, db.QueryRow(`SELECT phase_id FROM tasks WHERE id='C4'`).Scan(&phase))
	require.Equal(t, "ph-2", phase.String)
	var repairs int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM plan_phase_repairs`).Scan(&repairs))
	require.Equal(t, 2, repairs)

	_, err = db.Exec(`INSERT INTO tasks (id, title, parent_id, phase_id) VALUES ('X1','x','P1','ph-7')`)
	require.ErrorContains(t, err, "invalid phase")
	_, err = db.Exec(`INSERT INTO tasks (id, title, parent_id, phase_id) VALUES ('X1','x','P1','ph-1')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tasks SET parent_id = NULL WHERE id = 'X1'`)
	require.ErrorContains(t, err, "invalid phase")

	_, err = db.Exec(`DELETE FROM tasks WHERE id = 'P1'`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT phase_id FROM tasks WHERE id='X1'`).Scan(&phase))
	require.False(t, phase.Valid)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM plan_phases`).Scan(&n))
	require.Equal(t, 0, n)
}
