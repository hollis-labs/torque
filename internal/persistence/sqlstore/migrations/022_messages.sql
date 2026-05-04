-- 022 messages — durable substrate for the messaging.Store contract
-- (CW-20260503-0012, S1.2). Generic envelope persistence; typed semantics
-- (request/response/handoff/etc.) live in the S1.3 broker, not here.
--
-- Design:
--   * `messages` is one row per envelope. Routing fields (kind, channel,
--     thread_id, in_reply_to, from_*, to_*) are columns so the Store can
--     index/filter without parsing JSON. payload + metadata stay opaque
--     (TEXT carrying JSON) — apps decide payload schemas.
--   * Per-recipient lifecycle (delivered_at/consumed_at) lives in
--     `message_deliveries` to support symmetric multi-recipient semantics
--     when callers want them. v0.2 of the lib is single-recipient per
--     envelope; the deliveries row is keyed (message_id, recipient_urn) so
--     this is forward-compatible without migration churn.
--   * `canceled_at` on `messages` mirrors the lib's Cancel semantic
--     (idempotent dead-letter). Subscribe reads use this as a soft signal;
--     persistence is the source of truth.

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,             -- UUIDv7 assigned by Store.Send
    kind            TEXT NOT NULL,                -- request|response|notice|status_update|handoff|escalation
    channel         TEXT NOT NULL DEFAULT '',
    thread_id       TEXT NOT NULL DEFAULT '',
    in_reply_to     TEXT NOT NULL DEFAULT '',

    from_kind       TEXT NOT NULL,                -- agent|user|service|session|workflow
    from_authority  TEXT NOT NULL,
    from_id         TEXT NOT NULL,
    from_subid      TEXT NOT NULL DEFAULT '',
    from_urn        TEXT NOT NULL,                -- denormalized canonical URN for indexed lookups

    to_kind         TEXT NOT NULL,
    to_authority    TEXT NOT NULL,
    to_id           TEXT NOT NULL,
    to_subid        TEXT NOT NULL DEFAULT '',
    to_urn          TEXT NOT NULL,

    payload         BLOB,                         -- json.RawMessage; opaque to this layer
    content_type    TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',   -- map<string,string> as JSON

    created_at      DATETIME NOT NULL,            -- assigned by Store.Send (UTC)
    canceled_at     DATETIME                      -- non-NULL = Cancel was called
);

CREATE INDEX IF NOT EXISTS idx_messages_to_urn_created
    ON messages(to_urn, created_at);

CREATE INDEX IF NOT EXISTS idx_messages_thread
    ON messages(thread_id, created_at)
    WHERE thread_id <> '';

CREATE INDEX IF NOT EXISTS idx_messages_in_reply_to
    ON messages(in_reply_to)
    WHERE in_reply_to <> '';

CREATE INDEX IF NOT EXISTS idx_messages_kind
    ON messages(kind);

CREATE TABLE IF NOT EXISTS message_deliveries (
    message_id    TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    recipient_urn TEXT NOT NULL,
    delivered_at  DATETIME NOT NULL,
    consumed_at   DATETIME,
    PRIMARY KEY (message_id, recipient_urn)
);

CREATE INDEX IF NOT EXISTS idx_message_deliveries_recipient
    ON message_deliveries(recipient_urn, delivered_at);
