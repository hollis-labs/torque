-- Scheduler tracking columns on tasks
ALTER TABLE tasks ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN last_run_id INTEGER REFERENCES runs(id);
ALTER TABLE tasks ADD COLUMN escalation_step INTEGER NOT NULL DEFAULT 0;

-- Worker heartbeats
CREATE TABLE IF NOT EXISTS worker_heartbeats (
    worker_id       TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    run_id          INTEGER NOT NULL REFERENCES runs(id),
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat  DATETIME DEFAULT CURRENT_TIMESTAMP,
    executor        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_heartbeats_task ON worker_heartbeats(task_id);

-- Cost ledger: per-run cost entries, aggregatable by task/sprint/global
CREATE TABLE IF NOT EXISTS cost_ledger (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    run_id          INTEGER NOT NULL REFERENCES runs(id),
    sprint_id       TEXT,
    cost            REAL NOT NULL DEFAULT 0,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    recorded_at     DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cost_task ON cost_ledger(task_id);
CREATE INDEX IF NOT EXISTS idx_cost_sprint ON cost_ledger(sprint_id);

-- Queue jobs: go-queue uses its own DB file, but we track job mapping here
CREATE TABLE IF NOT EXISTS scheduler_jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    run_id          INTEGER NOT NULL REFERENCES runs(id),
    queue_job_id    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued',
    enqueued_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME
);

CREATE INDEX IF NOT EXISTS idx_sched_jobs_task ON scheduler_jobs(task_id);
CREATE INDEX IF NOT EXISTS idx_sched_jobs_status ON scheduler_jobs(status);
