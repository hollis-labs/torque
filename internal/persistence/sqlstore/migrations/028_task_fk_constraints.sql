-- 028 promote tasks.sprint_id/project_id/epic_id to real FKs (FK-002).
--
-- These three columns have been plain TEXT since 004_enrich_entities.sql —
-- existence was only checked in Go at write time (internal/service), so a
-- deleted sprint/project/epic could leave a dangling reference SQLite never
-- caught. FK-001 (torque fk-orphan-cleanup) shipped the one-time backfill
-- that nulls out any such dangling values; this migration adds the actual
-- REFERENCES ... ON DELETE SET NULL constraints so it can't happen again,
-- matching the existing tasks.parent_id precedent
-- (parent_id TEXT REFERENCES tasks(id) ON DELETE SET NULL, added in
-- 013_task_parent_id.sql). Note tasks.collection_id (020_collections.sql)
-- is REFERENCES collections(id) with no ON DELETE clause (SQLite default
-- NO ACTION) — parent_id, not collection_id, is the actual ON DELETE SET
-- NULL precedent in this schema.
--
-- SQLite cannot ALTER a column to add a REFERENCES clause; full table
-- rebuild required. Column list reproduces the current tasks schema
-- exactly (025_kind_issue.sql's rebuild plus 026's launch_profile and
-- 027 had no effect on tasks) — the only changes are the REFERENCES
-- clauses on sprint_id/project_id/epic_id.
--
-- Live databases have child rows in runs, artifacts, worker_heartbeats,
-- cost_ledger, scheduler_jobs, worktrees, task_tags, checkpoints, and
-- tasks itself (parent_id) pointing at tasks.id. Defer FK checks until
-- commit so the drop/rename swap can complete with the final tasks table
-- present, matching 024_default_executor_cli.sql's precedent for
-- rebuilding this same table.
--
-- IMPORTANT: run `torque fk-orphan-cleanup` (FK-001) against any real
-- database before this migration reaches it. migrations.Run's post-step
-- `PRAGMA foreign_key_check` will fail this migration (and roll it back)
-- if any tasks row still references a missing sprint/project/epic.

PRAGMA defer_foreign_keys = ON;

CREATE TABLE tasks_new (
    id                       TEXT PRIMARY KEY,
    title                    TEXT NOT NULL,
    description              TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL DEFAULT 'todo',
    priority                 INTEGER NOT NULL DEFAULT 2,
    manual                   INTEGER NOT NULL DEFAULT 0,
    executor                 TEXT NOT NULL DEFAULT 'cli',
    agent_profile            TEXT NOT NULL DEFAULT '',
    working_dir              TEXT NOT NULL DEFAULT '',
    tools                    TEXT,
    permissions              TEXT,
    environment              TEXT,
    system_prompt            TEXT NOT NULL DEFAULT '',
    files                    TEXT,
    cost_budget              REAL,
    max_retries              INTEGER NOT NULL DEFAULT 3,
    max_duration_ms          INTEGER,
    token_budget             INTEGER,
    on_done                  TEXT NOT NULL DEFAULT 'review',
    on_fail                  TEXT NOT NULL DEFAULT 'retry',
    on_review                TEXT NOT NULL DEFAULT 'pause',
    escalation_chain         TEXT,
    quality_gates            TEXT,
    deliverables             TEXT,
    deliverable_preset       TEXT NOT NULL DEFAULT '',
    on_done_merge            TEXT NOT NULL DEFAULT 'none',
    depends_on               TEXT,
    blocked_reason           TEXT NOT NULL DEFAULT '',
    metadata                 TEXT,
    sprint_id                TEXT REFERENCES sprints(id) ON DELETE SET NULL,
    project_id               TEXT REFERENCES projects(id) ON DELETE SET NULL,
    epic_id                  TEXT REFERENCES epics(id) ON DELETE SET NULL,
    created_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
    retry_count              INTEGER NOT NULL DEFAULT 0,
    last_run_id              INTEGER REFERENCES runs(id),
    escalation_step          INTEGER NOT NULL DEFAULT 0,
    kind                     TEXT NOT NULL DEFAULT 'agent'
      CHECK (kind IN ('agent','external','wait','decision','parent','plan','internal','issue')),
    source_type              TEXT NOT NULL DEFAULT 'user'
      CHECK (source_type IN ('agent','user','api','system','webhook','import')),
    source_ref               TEXT,
    trust                    TEXT NOT NULL DEFAULT 'normal'
      CHECK (trust IN ('trusted','normal','untrusted')),
    checkpoint_mode          TEXT NOT NULL DEFAULT 'none'
      CHECK (checkpoint_mode IN ('none','blocking','non_blocking')),
    on_checkpoint_response   TEXT NOT NULL DEFAULT 'resume'
      CHECK (on_checkpoint_response IN ('resume','review','custom')),
    agent_file               TEXT NOT NULL DEFAULT '',
    parent_id                TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    subtodos                 TEXT,
    collection_id            TEXT REFERENCES collections(id),
    collection_position      INTEGER,
    added_to_collections_at  DATETIME,
    launch_profile           TEXT NOT NULL DEFAULT ''
);

INSERT INTO tasks_new
SELECT
    id, title, description, status, priority, manual, executor,
    agent_profile, working_dir, tools, permissions, environment,
    system_prompt, files, cost_budget, max_retries, max_duration_ms,
    token_budget, on_done, on_fail, on_review, escalation_chain,
    quality_gates, deliverables, deliverable_preset, on_done_merge,
    depends_on, blocked_reason, metadata, sprint_id, project_id, epic_id,
    created_at, updated_at, retry_count, last_run_id, escalation_step,
    kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response,
    agent_file, parent_id, subtodos,
    collection_id, collection_position, added_to_collections_at,
    launch_profile
FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

CREATE INDEX IF NOT EXISTS idx_tasks_status              ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority            ON tasks(priority);
CREATE INDEX IF NOT EXISTS idx_tasks_sprint              ON tasks(sprint_id);
CREATE INDEX IF NOT EXISTS idx_tasks_project             ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_epic                ON tasks(epic_id);
CREATE INDEX IF NOT EXISTS idx_tasks_kind_status         ON tasks(kind, status);
CREATE INDEX IF NOT EXISTS idx_tasks_source              ON tasks(source_type, source_ref);
CREATE INDEX IF NOT EXISTS idx_tasks_checkpoint_mode     ON tasks(checkpoint_mode);
CREATE INDEX IF NOT EXISTS idx_tasks_parent_id           ON tasks(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_collection_position ON tasks(collection_id, collection_position);
CREATE INDEX IF NOT EXISTS idx_tasks_added_to_collections
    ON tasks(added_to_collections_at)
    WHERE added_to_collections_at IS NOT NULL;
