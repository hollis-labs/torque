-- Nullable epic membership; existing sprints remain unassociated.
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS epic_id TEXT REFERENCES epics(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_sprints_epic_id ON sprints(epic_id);
