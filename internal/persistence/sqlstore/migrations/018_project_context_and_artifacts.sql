ALTER TABLE projects ADD COLUMN agent_path TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN read_paths TEXT;
ALTER TABLE projects ADD COLUMN write_paths TEXT;
ALTER TABLE projects ADD COLUMN context_paths TEXT;
ALTER TABLE projects ADD COLUMN permissions TEXT;
ALTER TABLE projects ADD COLUMN rules TEXT;

CREATE TABLE IF NOT EXISTS project_artifacts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL,
    entry_type  TEXT NOT NULL DEFAULT 'document',
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    file_path   TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    permissions TEXT,
    rules       TEXT,
    metadata    TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_project_artifacts_project ON project_artifacts(project_id);
