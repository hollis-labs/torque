-- 042 event_outbox (Postgres override; same columns): see the SQLite file for
-- the design. NOT SAFE TO READ BY PLAIN seq ON POSTGRES: concurrent
-- transactions can commit out of seq order, so a reader could skip a row. The
-- outbox reader therefore refuses to start on Postgres until seq is allocated
-- from a counter row updated last in the transaction (CW-20261016-0133).

CREATE TABLE IF NOT EXISTS event_outbox (
    seq            BIGSERIAL PRIMARY KEY,
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
    run_id         BIGINT,
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
    generation     BIGINT NOT NULL DEFAULT 1,
    pruned_through BIGINT NOT NULL DEFAULT 0
);
INSERT INTO event_outbox_meta (id, generation, pruned_through) VALUES (1, 1, 0) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS event_outbox_cursors (
    consumer   TEXT PRIMARY KEY,
    last_seq   BIGINT NOT NULL,
    updated_at TEXT NOT NULL
);
