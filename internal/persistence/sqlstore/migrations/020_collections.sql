-- 020 collections — kanban-style task containers with positional ordering plus
-- an inbox semantic. Feature-flagged behind features.collections.
--
-- Design:
--   * `collections` is a flat table — collections do not nest. Archive replaces
--     delete for audit; archived_at = NULL means active.
--   * Tasks join via tasks.collection_id (NULL = not in any collection) plus a
--     numeric collection_position used for explicit reordering within a
--     collection.
--   * tasks.added_to_collections_at is the inbox sentinel: NULL = legacy /
--     untouched (invisible to the collections view), non-NULL = participates
--     in the collections world. Inbox view is
--     `added_to_collections_at IS NOT NULL AND collection_id IS NULL`;
--     both NULL means legacy/untouched (NOT inbox).
--     Write-once: callers set it on first add to inbox or to a collection and
--     never clear it; remove-from-collection returns the task to inbox by
--     clearing collection_id only.
--   * Existing tasks have added_to_collections_at = NULL → no migration impact
--     on legacy data, no surprise inbox entries on day one.

CREATE TABLE IF NOT EXISTS collections (
    id          TEXT PRIMARY KEY,             -- COL-YYYYMMDD-NNNN
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    archived_at DATETIME,                     -- NULL = active
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_collections_archived ON collections(archived_at);

ALTER TABLE tasks ADD COLUMN collection_id TEXT REFERENCES collections(id);
ALTER TABLE tasks ADD COLUMN collection_position INTEGER;
ALTER TABLE tasks ADD COLUMN added_to_collections_at DATETIME;

CREATE INDEX IF NOT EXISTS idx_tasks_collection_position
    ON tasks(collection_id, collection_position);

CREATE INDEX IF NOT EXISTS idx_tasks_added_to_collections
    ON tasks(added_to_collections_at)
    WHERE added_to_collections_at IS NOT NULL;
