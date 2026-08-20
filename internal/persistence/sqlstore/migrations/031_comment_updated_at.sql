-- 031 comment_updated_at — ENT-COMMENT adds Update/Delete to comments (no
-- correction path existed before). This tracks when a comment was last
-- edited, mirroring tasks.updated_at.
--
-- Existing rows backfill to created_at (no prior edit history to
-- reconstruct — the row has never been edited). New rows default to
-- CURRENT_TIMESTAMP like created_at, then UpdateComment overwrites it on
-- each edit.
--
-- SQLite rejects a non-constant default (CURRENT_TIMESTAMP) on
-- ALTER TABLE ADD COLUMN ("Cannot add a column with non-constant default"),
-- so — same as migration 019's rationale for this exact table — this
-- rebuilds `comments` instead of a plain ALTER TABLE. Column list and index
-- reproduce 019's post-migration schema (the only comments-schema change
-- since then) plus the new updated_at column.
CREATE TABLE comments_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT    NOT NULL DEFAULT 'task',
    entity_id   TEXT    NOT NULL DEFAULT '',
    author      TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO comments_new (id, entity_type, entity_id, author, content, created_at, updated_at)
SELECT id, entity_type, entity_id, author, content, created_at, created_at FROM comments;

DROP TABLE comments;
ALTER TABLE comments_new RENAME TO comments;

CREATE INDEX IF NOT EXISTS idx_comments_entity
    ON comments(entity_type, entity_id, created_at);
