-- 024 flip tasks.executor DEFAULT from 'opencode' to 'cli'.
--
-- Migration 016 (2026-04-XX) set the default to 'opencode' when opencode
-- was the dogfood executor. Dogfood flipped to codex on 2026-05-10 (per
-- ~/.torque/dogfood/profiles.yaml header: "after the Codex + Opencode
-- parity work landed (CW-20260510-0065/0067)"). Codex profiles use
-- `executor: cli`, but the task-creation default never followed — every
-- torque_task_create with no explicit executor returned
-- Executor="opencode" even when the chosen agent_profile was
-- configured `executor: cli`. The MCP tool docstring already claims
-- "default cli"; this migration makes reality match the doc.
--
-- Captured in Vanta as followups_torque_executor_default_drift; this
-- is the (a) layer of the three-layer fix. The service-layer fallback
-- (internal/service/task.go:154-160 effectiveExecutor = "opencode") is
-- the (b) layer, dropped in a sibling commit. No compat shim per
-- feedback_no_compat_shims — codex is the dogfood path, no external
-- consumer depends on the prior default.
--
-- SQLite cannot ALTER a column's DEFAULT; full table rebuild required.
-- Column list reproduces 021's schema exactly (no other schema drift on
-- the tasks table since 021 — migrations 022/023 added messages +
-- sessions, neither touched tasks). Existing rows keep their current
-- executor value (per-row data unchanged); only the default for
-- future inserts changes.
--
-- Live databases can have child rows in runs, comments, artifacts, sessions,
-- and related tables pointing at tasks.id. Defer FK checks until commit so the
-- drop/rename swap can complete with the final tasks table present.

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
    sprint_id                TEXT,
    project_id               TEXT,
    epic_id                  TEXT,
    created_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
    retry_count              INTEGER NOT NULL DEFAULT 0,
    last_run_id              INTEGER REFERENCES runs(id),
    escalation_step          INTEGER NOT NULL DEFAULT 0,
    kind                     TEXT NOT NULL DEFAULT 'agent'
      CHECK (kind IN ('agent','external','wait','decision','parent','plan','internal')),
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
    added_to_collections_at  DATETIME
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
    collection_id, collection_position, added_to_collections_at
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
