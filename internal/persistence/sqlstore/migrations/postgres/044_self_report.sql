-- 044: self-report settings and durable server-side bookkeeping (no events yet).
-- NULL settings inherit; provenance is stored on the instantiated task.
ALTER TABLE tasks ADD COLUMN progress_detail TEXT;
ALTER TABLE tasks ADD COLUMN progress_detail_source TEXT;
ALTER TABLE task_templates ADD COLUMN progress_detail TEXT;
ALTER TABLE runs ADD COLUMN progress_detail TEXT;
ALTER TABLE sessions ADD COLUMN progress_detail TEXT;
-- Tether-owned correlation: intentionally NULL until a verified binding
-- is available. Torque session ids and provider ids are not substitutes.
ALTER TABLE runs ADD COLUMN tether_session_id TEXT;
ALTER TABLE runs ADD COLUMN launch_id TEXT;
ALTER TABLE runs ADD COLUMN workstream_id TEXT;
ALTER TABLE sessions ADD COLUMN tether_session_id TEXT;
ALTER TABLE sessions ADD COLUMN launch_id TEXT;
ALTER TABLE sessions ADD COLUMN workstream_id TEXT;
ALTER TABLE sessions ADD COLUMN run_id BIGINT REFERENCES runs(id) ON DELETE SET NULL;
CREATE INDEX idx_sessions_run_id ON sessions(run_id);

-- Nullable so a rollback to an older binary can still insert runs.
ALTER TABLE runs ADD COLUMN attempt INTEGER CHECK (attempt > 0);

-- Existing attempts are ordinals of surviving run rows, not reconstructed
-- history of deleted runs. Every status counts, ordered by the stable run id.
WITH numbered AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY id) AS n FROM runs
)
UPDATE runs SET attempt = (SELECT n FROM numbered WHERE numbered.id = runs.id);
CREATE UNIQUE INDEX idx_runs_task_attempt ON runs(task_id, attempt);

CREATE TABLE task_run_counters (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    last_attempt INTEGER NOT NULL CHECK (last_attempt > 0)
);
INSERT INTO task_run_counters(task_id, last_attempt)
    SELECT task_id, MAX(attempt) FROM runs GROUP BY task_id;

-- Only emitted reports acquire receipts. The shared key space spans both
-- tools; tool_kind + request_hash detect cross-tool/different-input reuse.
-- event_id is the canonical envelope id, not the numeric replay cursor.
CREATE TABLE task_self_report_receipts (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    tool_kind TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    event_id TEXT NOT NULL,
    data_ref TEXT,
    response_json TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(task_id, idempotency_key)
);
CREATE TABLE task_progress_rate (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    detail_level TEXT NOT NULL CHECK (detail_level IN ('milestone','step','verbose')),
    last_emitted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(task_id, detail_level)
);
