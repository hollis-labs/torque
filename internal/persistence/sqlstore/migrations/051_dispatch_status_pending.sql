-- Ingestion can pass unresolved identities/formation while the retention cursor
-- stays behind the oldest pending delivery. Source envelopes and sealed bodies
-- remain in event_outbox until every preceding delivery is settled.
CREATE TABLE dispatch_status_pending (
    seq INTEGER PRIMARY KEY REFERENCES event_outbox(seq) ON DELETE RESTRICT,
    attempts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX dispatch_status_pending_fair ON dispatch_status_pending(attempts,seq);
