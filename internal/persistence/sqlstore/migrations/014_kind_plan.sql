-- 014 widen tasks.kind CHECK to include 'plan' for Plans v1
-- (CW-20260417-0374). Rebuilds the tasks table with the new constraint;
-- full column list is reproduced so existing data copies 1:1. Indexes are
-- recreated afterwards. FK references from other tables (task_tags,
-- run_events, comments, etc.) survive because SQLite resolves them by name.

CREATE TABLE tasks_new (
    id                     TEXT PRIMARY KEY,
    title                  TEXT NOT NULL,
    description            TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL DEFAULT 'todo',
    priority               INTEGER NOT NULL DEFAULT 2,
    manual                 INTEGER NOT NULL DEFAULT 0,
    executor               TEXT NOT NULL DEFAULT 'cli',
    agent_profile          TEXT NOT NULL DEFAULT '',
    working_dir            TEXT NOT NULL DEFAULT '',
    tools                  TEXT,
    permissions            TEXT,
    environment            TEXT,
    system_prompt          TEXT NOT NULL DEFAULT '',
    files                  TEXT,
    cost_budget            REAL,
    max_retries            INTEGER NOT NULL DEFAULT 3,
    max_duration_ms        INTEGER,
    token_budget           INTEGER,
    on_done                TEXT NOT NULL DEFAULT 'review',
    on_fail                TEXT NOT NULL DEFAULT 'retry',
    on_review              TEXT NOT NULL DEFAULT 'pause',
    escalation_chain       TEXT,
    quality_gates          TEXT,
    deliverables           TEXT,
    deliverable_preset     TEXT NOT NULL DEFAULT '',
    on_done_merge          TEXT NOT NULL DEFAULT 'none',
    depends_on             TEXT,
    blocked_reason         TEXT NOT NULL DEFAULT '',
    metadata               TEXT,
    sprint_id              TEXT,
    project_id             TEXT,
    epic_id                TEXT,
    created_at             DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at             DATETIME DEFAULT CURRENT_TIMESTAMP,
    retry_count            INTEGER NOT NULL DEFAULT 0,
    last_run_id            INTEGER REFERENCES runs(id),
    escalation_step        INTEGER NOT NULL DEFAULT 0,
    kind                   TEXT NOT NULL DEFAULT 'agent'
      CHECK (kind IN ('agent','external','wait','decision','parent','plan')),
    source_type            TEXT NOT NULL DEFAULT 'user'
      CHECK (source_type IN ('agent','user','api','system','webhook','import')),
    source_ref             TEXT,
    trust                  TEXT NOT NULL DEFAULT 'normal'
      CHECK (trust IN ('trusted','normal','untrusted')),
    checkpoint_mode        TEXT NOT NULL DEFAULT 'none'
      CHECK (checkpoint_mode IN ('none','blocking','non_blocking')),
    on_checkpoint_response TEXT NOT NULL DEFAULT 'resume'
      CHECK (on_checkpoint_response IN ('resume','review','custom')),
    agent_file             TEXT NOT NULL DEFAULT '',
    parent_id              TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    subtodos               TEXT
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
    agent_file, parent_id, subtodos
FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

CREATE INDEX IF NOT EXISTS idx_tasks_status            ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority          ON tasks(priority);
CREATE INDEX IF NOT EXISTS idx_tasks_sprint            ON tasks(sprint_id);
CREATE INDEX IF NOT EXISTS idx_tasks_project           ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_epic              ON tasks(epic_id);
CREATE INDEX IF NOT EXISTS idx_tasks_kind_status       ON tasks(kind, status);
CREATE INDEX IF NOT EXISTS idx_tasks_source            ON tasks(source_type, source_ref);
CREATE INDEX IF NOT EXISTS idx_tasks_checkpoint_mode   ON tasks(checkpoint_mode);
CREATE INDEX IF NOT EXISTS idx_tasks_parent_id         ON tasks(parent_id) WHERE parent_id IS NOT NULL;
