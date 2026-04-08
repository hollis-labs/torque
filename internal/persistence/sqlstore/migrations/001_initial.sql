-- Tasks: the first-class citizen
CREATE TABLE IF NOT EXISTS tasks (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'todo',
    priority        INTEGER NOT NULL DEFAULT 2,
    tags            TEXT NOT NULL DEFAULT '[]',
    manual          INTEGER NOT NULL DEFAULT 0,
    executor        TEXT NOT NULL DEFAULT 'cli',
    agent_profile   TEXT NOT NULL DEFAULT '',
    working_dir     TEXT NOT NULL DEFAULT '',
    tools           TEXT,
    permissions     TEXT,
    environment     TEXT,
    system_prompt   TEXT NOT NULL DEFAULT '',
    files           TEXT,
    cost_budget     REAL,
    max_retries     INTEGER NOT NULL DEFAULT 3,
    max_duration_ms INTEGER,
    token_budget    INTEGER,
    on_done         TEXT NOT NULL DEFAULT 'review',
    on_fail         TEXT NOT NULL DEFAULT 'retry',
    on_review       TEXT NOT NULL DEFAULT 'pause',
    escalation_chain TEXT,
    quality_gates   TEXT,
    deliverables    TEXT,
    deliverable_preset TEXT NOT NULL DEFAULT '',
    on_done_merge   TEXT NOT NULL DEFAULT 'none',
    depends_on      TEXT,
    blocked_reason  TEXT NOT NULL DEFAULT '',
    metadata        TEXT,
    sprint_id       TEXT,
    project_id      TEXT,
    epic_id         TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority);
CREATE INDEX IF NOT EXISTS idx_tasks_sprint ON tasks(sprint_id);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_epic ON tasks(epic_id);

-- Runs: execution history
CREATE TABLE IF NOT EXISTS runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    executor        TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'running',
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost            REAL NOT NULL DEFAULT 0,
    exit_code       INTEGER,
    error_message   TEXT NOT NULL DEFAULT '',
    metadata        TEXT
);

CREATE INDEX IF NOT EXISTS idx_runs_task ON runs(task_id);

-- Artifacts: task-attached evidence
CREATE TABLE IF NOT EXISTS artifacts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    run_id          INTEGER REFERENCES runs(id),
    type            TEXT NOT NULL,
    content         TEXT NOT NULL DEFAULT '',
    url             TEXT NOT NULL DEFAULT '',
    file_path       TEXT NOT NULL DEFAULT '',
    metadata        TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_run ON artifacts(run_id);

-- Comments
CREATE TABLE IF NOT EXISTS comments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    author          TEXT NOT NULL DEFAULT '',
    content         TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_task ON comments(task_id);

-- Settings
CREATE TABLE IF NOT EXISTS settings (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Sprints (table always exists, feature-gated at app layer)
CREATE TABLE IF NOT EXISTS sprints (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    goal            TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'planning',
    approval_mode   TEXT NOT NULL DEFAULT 'approve_each',
    cost_budget     REAL,
    started_at      DATETIME,
    ended_at        DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Projects (table always exists, feature-gated at app layer)
CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    repo_path       TEXT NOT NULL DEFAULT '',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Epics (table always exists, feature-gated at app layer)
CREATE TABLE IF NOT EXISTS epics (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
