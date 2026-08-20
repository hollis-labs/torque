-- 031 comment_updated_at — ENT-COMMENT adds Update/Delete to comments (no
-- correction path existed before). This tracks when a comment was last
-- edited, mirroring tasks.updated_at.
--
-- Existing rows backfill to created_at (no prior edit history to
-- reconstruct — the row has never been edited). New rows default to
-- CURRENT_TIMESTAMP like created_at, then UpdateComment overwrites it on
-- each edit.
ALTER TABLE comments ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP;

UPDATE comments SET updated_at = created_at;
