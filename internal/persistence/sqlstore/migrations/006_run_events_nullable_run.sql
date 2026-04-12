-- Make run_events.run_id nullable so lifecycle transitions that happen
-- before a run exists (e.g., todo → doing scheduling, or task-level
-- transitions with no active run) can still be logged.
CREATE TABLE run_events_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER REFERENCES runs(id),
    task_id     TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO run_events_new (id, run_id, task_id, type, payload, created_at)
    SELECT id, run_id, task_id, type, payload, created_at FROM run_events;

DROP TABLE run_events;
ALTER TABLE run_events_new RENAME TO run_events;

CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id);
CREATE INDEX IF NOT EXISTS idx_run_events_task ON run_events(task_id);
CREATE INDEX IF NOT EXISTS idx_run_events_type ON run_events(type);
