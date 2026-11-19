-- Optional external identity; NULL/NULL preserves existing project behavior.
ALTER TABLE projects ADD COLUMN external_authority TEXT;
ALTER TABLE projects ADD COLUMN external_id TEXT CHECK (
 (external_authority IS NULL AND external_id IS NULL) OR
 (external_authority IS NOT NULL AND external_id IS NOT NULL AND
  length(trim(external_authority)) > 0 AND length(trim(external_id)) > 0)
);
CREATE UNIQUE INDEX projects_external_reference ON projects(external_authority, external_id)
 WHERE external_authority IS NOT NULL;
