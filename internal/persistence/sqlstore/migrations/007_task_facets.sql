-- Add task-facet columns per spec 2026-04-16-task-model-mvp-design.md §3.1.
-- SQLite ALTER TABLE ADD COLUMN NOT NULL requires a DEFAULT; service layer still
-- applies defaults explicitly via orDefault — SQL defaults are a safety net.

ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent'
  CHECK (kind IN ('agent','external','wait','decision','parent'));
ALTER TABLE tasks ADD COLUMN source_type TEXT NOT NULL DEFAULT 'user'
  CHECK (source_type IN ('agent','user','api','system','webhook','import'));
ALTER TABLE tasks ADD COLUMN source_ref TEXT;
ALTER TABLE tasks ADD COLUMN trust TEXT NOT NULL DEFAULT 'normal'
  CHECK (trust IN ('trusted','normal','untrusted'));
ALTER TABLE tasks ADD COLUMN checkpoint_mode TEXT NOT NULL DEFAULT 'none'
  CHECK (checkpoint_mode IN ('none','blocking','non_blocking'));
ALTER TABLE tasks ADD COLUMN on_checkpoint_response TEXT NOT NULL DEFAULT 'resume'
  CHECK (on_checkpoint_response IN ('resume','review','custom'));

CREATE INDEX IF NOT EXISTS idx_tasks_kind_status     ON tasks(kind, status);
CREATE INDEX IF NOT EXISTS idx_tasks_source          ON tasks(source_type, source_ref);
CREATE INDEX IF NOT EXISTS idx_tasks_checkpoint_mode ON tasks(checkpoint_mode);
