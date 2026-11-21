-- Ops system of record (ops/v2 contract, caster://ops/design.system-of-record-v2
-- section 3.1). A task without an Ops item is linked by inheritance or, failing
-- that, by an idempotent ops item_intake call. Intakes that could not complete
-- (Ops down, tool not deployed yet) wait here and are retried with backoff; the
-- create that produced them is never refused.
CREATE TABLE ops_intake_pending (
    task_id     TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    intake_key  TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_at     TEXT NOT NULL DEFAULT '',
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ops_intake_pending_due ON ops_intake_pending(next_at, attempts);
