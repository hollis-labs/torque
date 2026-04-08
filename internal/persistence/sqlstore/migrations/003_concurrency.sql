-- Hot event log: drained from queue.db into this table for persistence.
-- The write buffer drain goroutine batch-inserts here.
CREATE TABLE IF NOT EXISTS run_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER NOT NULL REFERENCES runs(id),
    task_id     TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id);
CREATE INDEX IF NOT EXISTS idx_run_events_task ON run_events(task_id);
CREATE INDEX IF NOT EXISTS idx_run_events_type ON run_events(type);

-- Worktree tracking: active worktrees per task execution.
CREATE TABLE IF NOT EXISTS worktrees (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    run_id          INTEGER REFERENCES runs(id),
    project_id      TEXT,
    branch          TEXT NOT NULL,
    path            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    cleaned_at      DATETIME
);

CREATE INDEX IF NOT EXISTS idx_worktrees_task ON worktrees(task_id);
CREATE INDEX IF NOT EXISTS idx_worktrees_project ON worktrees(project_id);
CREATE INDEX IF NOT EXISTS idx_worktrees_status ON worktrees(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_worktrees_branch ON worktrees(branch);
