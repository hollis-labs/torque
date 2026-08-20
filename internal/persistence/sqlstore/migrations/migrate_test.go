package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrationsApply(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = migrations.Run(db)
	require.NoError(t, err)

	// Verify 002 columns exist
	_, err = db.Exec("SELECT retry_count, last_run_id, escalation_step FROM tasks LIMIT 0")
	require.NoError(t, err)

	// Verify 002 tables exist
	_, err = db.Exec("SELECT worker_id, task_id, run_id, last_heartbeat FROM worker_heartbeats LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT id, task_id, run_id, cost, prompt_tokens, completion_tokens FROM cost_ledger LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT id, task_id, run_id, queue_job_id, status FROM scheduler_jobs LIMIT 0")
	require.NoError(t, err)

	// Verify 005 tables exist with expected columns
	_, err = db.Exec("SELECT slug, name, description, color, created_at, updated_at FROM tags LIMIT 0")
	require.NoError(t, err)

	_, err = db.Exec("SELECT task_id, tag_slug, sort_order, created_at FROM task_tags LIMIT 0")
	require.NoError(t, err)

	// Verify 005 dropped the legacy tasks.tags column
	_, err = db.Exec("SELECT tags FROM tasks LIMIT 0")
	require.Error(t, err, "tasks.tags column should have been dropped by migration 005")

	// Verify 006 made run_events.run_id nullable: inserting with NULL should succeed.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('T1', 'test', 'todo')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_events (run_id, task_id, type, payload) VALUES (NULL, 'T1', 'task_transitioned', '{}')`)
	require.NoError(t, err, "run_events.run_id should be nullable after migration 006")

	// Verify 007 added facet columns to tasks.
	_, err = db.Exec(`SELECT kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response FROM tasks LIMIT 0`)
	require.NoError(t, err, "tasks facet columns should exist after migration 007")

	// Verify 007 enforces kind CHECK constraint.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T7bad', 'bad', 'todo', 'nonsense')`)
	require.Error(t, err, "tasks.kind CHECK should reject unknown kinds")

	// Verify 007 allows valid kinds with defaults for other facet columns.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T7ok', 'ok', 'todo', 'wait')`)
	require.NoError(t, err, "tasks.kind should accept 'wait'")

	// Verify 007 created the new indexes.
	idxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='tasks'`)
	require.NoError(t, err)
	defer idxRows.Close()
	idx := map[string]bool{}
	for idxRows.Next() {
		var n string
		require.NoError(t, idxRows.Scan(&n))
		idx[n] = true
	}
	require.True(t, idx["idx_tasks_kind_status"], "idx_tasks_kind_status should exist")
	require.True(t, idx["idx_tasks_source"], "idx_tasks_source should exist")
	require.True(t, idx["idx_tasks_checkpoint_mode"], "idx_tasks_checkpoint_mode should exist")

	// Verify 008 created the checkpoints table with expected columns.
	_, err = db.Exec(`SELECT id, task_id, run_id, correlation_id, type,
		payload_json, response_json, emitter_source_type, emitter_source_ref,
		responder_source_type, responder_source_ref, emitted_at, responded_at,
		timeout_at, status FROM checkpoints LIMIT 0`)
	require.NoError(t, err, "checkpoints table should exist after migration 008")

	// Verify 008 status CHECK constraint.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('CP-T1', 't', 'doing')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO checkpoints (task_id, correlation_id, type, payload_json, emitter_source_type, status)
		VALUES ('CP-T1', 'CORR-BAD', 'test', '{}', 'system', 'bogus')`)
	require.Error(t, err, "checkpoints.status CHECK should reject unknown statuses")

	// Verify 008 accepts a valid pending checkpoint.
	_, err = db.Exec(`INSERT INTO checkpoints (task_id, correlation_id, type, payload_json, emitter_source_type)
		VALUES ('CP-T1', 'CORR-OK', 'test', '{}', 'system')`)
	require.NoError(t, err, "checkpoints insert with default 'pending' status should succeed")

	// Verify 008 correlation_id UNIQUE.
	_, err = db.Exec(`INSERT INTO checkpoints (task_id, correlation_id, type, payload_json, emitter_source_type)
		VALUES ('CP-T1', 'CORR-OK', 'test', '{}', 'system')`)
	require.Error(t, err, "checkpoints.correlation_id should be UNIQUE")

	// Verify 008 indexes.
	cpRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='checkpoints'`)
	require.NoError(t, err)
	defer cpRows.Close()
	cpIdx := map[string]bool{}
	for cpRows.Next() {
		var n string
		require.NoError(t, cpRows.Scan(&n))
		cpIdx[n] = true
	}
	require.True(t, cpIdx["idx_checkpoints_task_status"], "idx_checkpoints_task_status should exist")
	require.True(t, cpIdx["idx_checkpoints_pending"], "idx_checkpoints_pending should exist")
	require.True(t, cpIdx["idx_checkpoints_correlation"], "idx_checkpoints_correlation should exist")
	// Cascade-delete behaviour is exercised indirectly via sqlstore tests
	// where Store.New() enables foreign_keys; this test uses a raw sql.Open
	// so PRAGMA foreign_keys is OFF by default.

	// Verify 009 task_templates table + composite primary key.
	_, err = db.Exec(`SELECT id, version, name, description, kind, auto_execute,
		executor, agent_profile, system_prompt, tools, permissions, environment,
		cost_budget, max_retries, max_duration_ms, token_budget,
		on_done, on_fail, on_review, on_done_merge,
		escalation_chain, quality_gates, deliverables,
		checkpoint_mode, on_checkpoint_response,
		metadata_template, required_vars, tags, is_archived, created_at, updated_at
		FROM task_templates LIMIT 0`)
	require.NoError(t, err, "task_templates columns should exist after migration 009")

	// Verify 009 allows independent versions under the same id.
	_, err = db.Exec(`INSERT INTO task_templates (id, version, name, description, kind)
		VALUES ('T', 1, 'v1', 'x', 'agent')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO task_templates (id, version, name, description, kind)
		VALUES ('T', 2, 'v2', 'x', 'agent')`)
	require.NoError(t, err, "distinct versions of the same id should be accepted")
	_, err = db.Exec(`INSERT INTO task_templates (id, version, name, description, kind)
		VALUES ('T', 1, 'dup', 'x', 'agent')`)
	require.Error(t, err, "duplicate (id, version) should be rejected by PK")

	// Verify the idx_templates_kind index exists.
	tplIdxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='task_templates'`)
	require.NoError(t, err)
	defer tplIdxRows.Close()
	tplIdx := map[string]bool{}
	for tplIdxRows.Next() {
		var n string
		require.NoError(t, tplIdxRows.Scan(&n))
		tplIdx[n] = true
	}
	require.True(t, tplIdx["idx_templates_kind"], "idx_templates_kind should exist")

	// Verify 010 added the working_dir column to task_templates
	// (decision outcome captured in CW-20260416-0003: pick A).
	_, err = db.Exec(`SELECT working_dir FROM task_templates LIMIT 0`)
	require.NoError(t, err, "task_templates.working_dir should exist after migration 010")

	// Existing rows should have a NULL default — the column is nullable so
	// migrations over populated DBs don't break.
	_, err = db.Exec(`INSERT INTO task_templates (id, version, name, description, kind)
		VALUES ('T-WD', 1, 'v1', 'x', 'agent')`)
	require.NoError(t, err)
	var wd sql.NullString
	err = db.QueryRow(`SELECT working_dir FROM task_templates WHERE id = 'T-WD'`).Scan(&wd)
	require.NoError(t, err)
	require.False(t, wd.Valid, "existing rows inserted without working_dir should have NULL, not ''")

	// Verify 012 added agent_file column to tasks and task_templates.
	// Column is NOT NULL DEFAULT '' so rows inserted before/after the migration
	// both default to empty string (no agent file).
	_, err = db.Exec(`SELECT agent_file FROM tasks LIMIT 0`)
	require.NoError(t, err, "tasks.agent_file should exist after migration 012")
	_, err = db.Exec(`SELECT agent_file FROM task_templates LIMIT 0`)
	require.NoError(t, err, "task_templates.agent_file should exist after migration 012")

	var af string
	err = db.QueryRow(`SELECT agent_file FROM tasks WHERE id = 'T1'`).Scan(&af)
	require.NoError(t, err)
	require.Equal(t, "", af, "existing rows should default to empty agent_file after migration 012")

	// Verify 013 added parent_id column + index.
	_, err = db.Exec(`SELECT parent_id FROM tasks LIMIT 0`)
	require.NoError(t, err, "tasks.parent_id should exist after migration 013")

	var pid sql.NullString
	err = db.QueryRow(`SELECT parent_id FROM tasks WHERE id = 'T1'`).Scan(&pid)
	require.NoError(t, err)
	require.False(t, pid.Valid, "existing rows should have NULL parent_id after migration 013")

	// parent_id index should exist (partial index on non-null rows).
	idxRows2, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='tasks'`)
	require.NoError(t, err)
	defer idxRows2.Close()
	parentIdx := map[string]bool{}
	for idxRows2.Next() {
		var n string
		require.NoError(t, idxRows2.Scan(&n))
		parentIdx[n] = true
	}
	require.True(t, parentIdx["idx_tasks_parent_id"], "idx_tasks_parent_id should exist")

	// Verify 014 widened the kind CHECK to accept 'plan'.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T14-plan', 'plan task', 'todo', 'plan')`)
	require.NoError(t, err, "tasks.kind should accept 'plan' after migration 014")
	_, err = db.Exec(`INSERT INTO tasks (id, title, status, kind) VALUES ('T14-bad', 'bad', 'todo', 'still-bad')`)
	require.Error(t, err, "tasks.kind CHECK should still reject unknown kinds after migration 014")

	// Verify 015 created the runs.status index (CW-20260418-0015 taxonomy).
	runsIdxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='runs'`)
	require.NoError(t, err)
	defer runsIdxRows.Close()
	runsIdx := map[string]bool{}
	for runsIdxRows.Next() {
		var n string
		require.NoError(t, runsIdxRows.Scan(&n))
		runsIdx[n] = true
	}
	require.True(t, runsIdx["idx_runs_status"], "idx_runs_status should exist after migration 015")

	// Verify 015 migration is idempotent: running migrations a second time
	// against the same DB is a no-op (schema_migrations short-circuits) and
	// must not error. This guards against a future rewrite of 015 that
	// drops/recreates the index non-idempotently.
	require.NoError(t, migrations.Run(db), "migrations must be idempotent — second run should no-op cleanly")

	// Re-executing just the 015 body against the same DB must also be
	// idempotent even if schema_migrations is bypassed — this is what the
	// ticket calls out explicitly ("forward-only, idempotent"). The
	// `CREATE INDEX IF NOT EXISTS` guards the single DDL operation.
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status)`)
	require.NoError(t, err, "015 index creation must tolerate re-run")

	// Verify runs.status accepts all documented taxonomy values. The column
	// has no CHECK constraint (widening via table rebuild is deferred) so
	// this test locks in the documented set — future code must accept all
	// of these and dashboards must segment cleanly.
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('T15-ok', 't', 'todo')`)
	require.NoError(t, err)
	for _, s := range []string{"running", "done", "failed", "blocked", "review", "cancelled", "superseded", "killed"} {
		_, err = db.Exec(`INSERT INTO runs (task_id, executor, status) VALUES ('T15-ok', 'cli', ?)`, s)
		require.NoError(t, err, "runs.status must accept taxonomy value %q", s)
	}

	// Verify 022 created the messages substrate (CW-20260503-0012, S1.2).
	_, err = db.Exec(`SELECT id, kind, channel, thread_id, in_reply_to,
		from_kind, from_authority, from_id, from_subid, from_urn,
		to_kind, to_authority, to_id, to_subid, to_urn,
		payload, content_type, metadata_json, created_at, canceled_at
		FROM messages LIMIT 0`)
	require.NoError(t, err, "messages table should exist after migration 022")

	_, err = db.Exec(`SELECT message_id, recipient_urn, delivered_at, consumed_at
		FROM message_deliveries LIMIT 0`)
	require.NoError(t, err, "message_deliveries table should exist after migration 022")

	msgIdxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='messages'`)
	require.NoError(t, err)
	defer msgIdxRows.Close()
	msgIdx := map[string]bool{}
	for msgIdxRows.Next() {
		var n string
		require.NoError(t, msgIdxRows.Scan(&n))
		msgIdx[n] = true
	}
	require.True(t, msgIdx["idx_messages_to_urn_created"], "idx_messages_to_urn_created should exist")
	require.True(t, msgIdx["idx_messages_thread"], "idx_messages_thread should exist")
	require.True(t, msgIdx["idx_messages_kind"], "idx_messages_kind should exist")

	// Verify 023 created sessions + session_checkpoints (CW-20260503-0014, S1.4).
	_, err = db.Exec(`SELECT id, agent_profile, provider, runtime_id, runtime_kind,
		workdir, project_id, task_id, state, pid, exit_code, resume_hint, meta,
		created_at, updated_at, last_activity, ended_at FROM sessions LIMIT 0`)
	require.NoError(t, err, "sessions schema should be present after 023")

	_, err = db.Exec(`SELECT id, session_id, payload, resume_hint, note, created_at
		FROM session_checkpoints LIMIT 0`)
	require.NoError(t, err, "session_checkpoints schema should be present after 023")

	_, err = db.Exec(`INSERT INTO sessions (id, state) VALUES ('S23-bad', 'nonsense')`)
	require.Error(t, err, "sessions.state CHECK should reject unknown values")

	for _, st := range []string{"launching", "running", "done", "failed", "crashed"} {
		_, err = db.Exec(`INSERT INTO sessions (id, state) VALUES (?, ?)`, "S23-"+st, st)
		require.NoError(t, err, "sessions.state should accept %q", st)
	}

	// Verify 027 created task_dependencies with the expected columns
	// (FK-003: depends_on promoted from a JSON column to a join table,
	// mirroring 005's tags -> task_tags promotion).
	_, err = db.Exec(`SELECT task_id, depends_on_task_id, sort_order, created_at FROM task_dependencies LIMIT 0`)
	require.NoError(t, err, "task_dependencies columns should exist after migration 027")

	// Verify 027 dropped the legacy tasks.depends_on column.
	_, err = db.Exec(`SELECT depends_on FROM tasks LIMIT 0`)
	require.Error(t, err, "tasks.depends_on column should have been dropped by migration 027")

	// Verify the composite PK rejects a duplicate (task_id, depends_on_task_id)
	// pair. Cascade-delete behavior is exercised indirectly via sqlstore tests
	// where Store.New() enables foreign_keys; this test uses a raw sql.Open so
	// PRAGMA foreign_keys is OFF by default (same caveat as the 008 comment
	// above).
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('T27-A', 'a', 'todo')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO tasks (id, title, status) VALUES ('T27-B', 'b', 'todo')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO task_dependencies (task_id, depends_on_task_id, sort_order, created_at)
		VALUES ('T27-B', 'T27-A', 0, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO task_dependencies (task_id, depends_on_task_id, sort_order, created_at)
		VALUES ('T27-B', 'T27-A', 1, CURRENT_TIMESTAMP)`)
	require.Error(t, err, "task_dependencies (task_id, depends_on_task_id) should be a UNIQUE composite PK")

	// Verify the depends_on_task_id index exists (mirrors idx_task_tags_tag_slug).
	depIdxRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='task_dependencies'`)
	require.NoError(t, err)
	defer depIdxRows.Close()
	depIdx := map[string]bool{}
	for depIdxRows.Next() {
		var n string
		require.NoError(t, depIdxRows.Scan(&n))
		depIdx[n] = true
	}
	require.True(t, depIdx["idx_task_dependencies_depends_on_task_id"], "idx_task_dependencies_depends_on_task_id should exist")
}
