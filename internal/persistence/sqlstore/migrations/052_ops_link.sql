-- Ops system of record (ops/v2 contract rev 3, caster://ops/design.system-of-record-v2
-- section 3.1). A task without an Ops item is linked at its first move to doing:
-- by inheritance, else to its project's unfiled item. Each unfiled item is created
-- once through the Ops service's idempotent item_intake; an intake that could not
-- complete (Ops down, tool not deployed yet) waits here and is retried with backoff.
-- Linking never waits on it: the unfiled id is deterministic.
CREATE TABLE ops_intake_pending (
    intake_key  TEXT PRIMARY KEY,
    item_id     TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    origin_task TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_at     TEXT NOT NULL DEFAULT '',
    last_error  TEXT NOT NULL DEFAULT '',
    done_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ops_intake_pending_due ON ops_intake_pending(done_at, next_at);
