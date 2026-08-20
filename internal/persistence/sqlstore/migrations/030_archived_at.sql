-- 030 archived_at — generic archive primitive for Project, Epic, Sprint.
--
-- Mirrors the collections.archived_at design (migration 020): a nullable
-- DATETIME column, NULL = active, non-NULL = archived. Archive is a concept
-- independent of `status` — archiving an epic isn't the same fact as the
-- epic being "done" (status is untouched by archive/unarchive).
--
-- Scope: PRIM-004 (ADR-0004 §3) requires this for Project, Epic, Sprint
-- only. Issue and Plan are rows in the shared `tasks` table; whether they
-- get a shared tasks.archived_at column or a narrower kind-scoped approach
-- is an open question flagged in the task file for separate confirmation,
-- not implemented here. Task itself keeps hard-delete + `abandoned` status
-- as its own audit-preserving close path and does not get archived_at.

ALTER TABLE projects ADD COLUMN archived_at DATETIME; -- NULL = active
ALTER TABLE epics    ADD COLUMN archived_at DATETIME; -- NULL = active
ALTER TABLE sprints  ADD COLUMN archived_at DATETIME; -- NULL = active

CREATE INDEX IF NOT EXISTS idx_projects_archived ON projects(archived_at);
CREATE INDEX IF NOT EXISTS idx_epics_archived    ON epics(archived_at);
CREATE INDEX IF NOT EXISTS idx_sprints_archived  ON sprints(archived_at);
