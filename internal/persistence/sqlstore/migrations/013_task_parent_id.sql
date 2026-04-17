-- 013 parent_id — per-task parent linkage for plans and sub-task trees.
-- parent_id = NULL means "top of lineage" (root). Cycles are prevented at
-- service layer (validateParentID). ON DELETE SET NULL keeps orphans alive
-- so children of a deleted plan stay visible for triage.

ALTER TABLE tasks ADD COLUMN parent_id TEXT REFERENCES tasks(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_parent_id ON tasks(parent_id) WHERE parent_id IS NOT NULL;
