-- 019 comments polymorphism — refactor `comments` from task-only to
-- polymorphic (entity_type + entity_id). Prerequisite for collection
-- commentary (CW-20260503-0004); also unlocks future comments on
-- epics/sprints/projects without further schema churn.
--
-- Strategy: SQLite cannot drop a FK constraint in place, so we
--   1) add the new columns
--   2) backfill entity_id from task_id (entity_type defaults to 'task')
--   3) recreate the table without task_id (and without the FK on tasks(id))
--   4) add a composite index on (entity_type, entity_id, created_at)

-- 1) Add new columns with safe defaults so existing rows remain valid.
ALTER TABLE comments ADD COLUMN entity_type TEXT NOT NULL DEFAULT 'task';
ALTER TABLE comments ADD COLUMN entity_id   TEXT NOT NULL DEFAULT '';

-- 2) Backfill: every existing row is a task comment.
UPDATE comments SET entity_id = task_id WHERE entity_id = '';

-- 3) Recreate the table without task_id and without the tasks(id) FK.
--    The old idx_comments_task index is dropped along with the table; we
--    replace it with the composite (entity_type, entity_id, created_at)
--    index below.
CREATE TABLE comments_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT    NOT NULL DEFAULT 'task',
    entity_id   TEXT    NOT NULL DEFAULT '',
    author      TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO comments_new (id, entity_type, entity_id, author, content, created_at)
SELECT id, entity_type, entity_id, author, content, created_at FROM comments;

DROP TABLE comments;
ALTER TABLE comments_new RENAME TO comments;

-- 4) Composite index for the canonical access pattern: list/search comments
--    for a given entity, ordered by time.
CREATE INDEX IF NOT EXISTS idx_comments_entity
    ON comments(entity_type, entity_id, created_at);
