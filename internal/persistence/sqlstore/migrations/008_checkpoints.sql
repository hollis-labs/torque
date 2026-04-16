-- 008 checkpoints — spec 2026-04-16-task-model-mvp-design.md §4.1.
-- Opaque payload/response JSON strings (go-envelope / BLG-030 owns schema
-- validation later). correlation_id is a ULID generated on emit.

CREATE TABLE checkpoints (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id                TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    run_id                 INTEGER REFERENCES runs(id) ON DELETE SET NULL,
    correlation_id         TEXT NOT NULL UNIQUE,
    type                   TEXT NOT NULL,
    payload_json           TEXT NOT NULL,
    response_json          TEXT,
    emitter_source_type    TEXT NOT NULL,
    emitter_source_ref     TEXT,
    responder_source_type  TEXT,
    responder_source_ref   TEXT,
    emitted_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    responded_at           TIMESTAMP,
    timeout_at             TIMESTAMP,
    status                 TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','responded','timed_out','canceled'))
);

CREATE INDEX idx_checkpoints_task_status  ON checkpoints(task_id, status);
CREATE INDEX idx_checkpoints_pending      ON checkpoints(status) WHERE status = 'pending';
CREATE INDEX idx_checkpoints_correlation  ON checkpoints(correlation_id);
