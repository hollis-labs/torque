-- 042 event_outbox: the transactional outbox for state events (CW-20261016-0133).
--
-- One row per state change (task.state_changed, and later run./checkpoint.),
-- written in the SAME transaction as the change itself, so a committed change
-- always has its event and a rolled-back one never does. Readers (SSE, later
-- the hub relay) consume it by `seq` and are at-least-once; consumers dedupe on
-- event_id.
--
--   * seq is Torque's local cursor, assigned in commit order: the SQLite writer
--     is a single BEGIN IMMEDIATE connection, so a reader that has seen seq N has
--     seen every smaller committed seq. That does NOT hold on Postgres.
--   * `envelope` is the stamped runtimeevents v1.1 JSON without the log-assigned
--     fields; the reader fills sequence (= seq) and generation on the way out.
--   * Scope columns hold only what Torque can verify from its own rows.
--   * generation (event_outbox_meta) is the producer restart counter; it is
--     bumped by hand on a restore or reset so a resuming reader gets a gap.

CREATE TABLE IF NOT EXISTS event_outbox (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id       TEXT NOT NULL UNIQUE,
    kind           TEXT NOT NULL,
    category       TEXT NOT NULL DEFAULT 'state',
    occurred_at    TEXT NOT NULL,
    entity_type    TEXT NOT NULL,
    entity_id      TEXT NOT NULL,
    project_id     TEXT,
    epic_id        TEXT,
    sprint_id      TEXT,
    task_id        TEXT,
    run_id         INTEGER,
    trigger_kind   TEXT NOT NULL,
    actor_urn      TEXT,
    envelope       TEXT NOT NULL,
    payload_digest TEXT,
    tombstoned_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_event_outbox_task    ON event_outbox(task_id, seq);
CREATE INDEX IF NOT EXISTS idx_event_outbox_project ON event_outbox(project_id, seq);
CREATE INDEX IF NOT EXISTS idx_event_outbox_kind    ON event_outbox(kind, seq);

CREATE TABLE IF NOT EXISTS event_outbox_meta (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    generation     INTEGER NOT NULL DEFAULT 1,
    pruned_through INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO event_outbox_meta (id, generation, pruned_through) VALUES (1, 1, 0);

CREATE TABLE IF NOT EXISTS event_outbox_cursors (
    consumer   TEXT PRIMARY KEY,
    last_seq   INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
