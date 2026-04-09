-- Enrich projects: add status and icon
ALTER TABLE projects ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE projects ADD COLUMN icon TEXT NOT NULL DEFAULT '';

-- Enrich sprints: add project_id
ALTER TABLE sprints ADD COLUMN project_id TEXT;

-- Enrich epics: add priority and project_id
ALTER TABLE epics ADD COLUMN priority INTEGER;
ALTER TABLE epics ADD COLUMN project_id TEXT;
