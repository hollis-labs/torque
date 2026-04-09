-- 005_tags.sql
-- Promote tags to a first-class relational entity.
-- Drops the legacy tasks.tags JSON-string column.

CREATE TABLE tags (
  slug        TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  color       TEXT NOT NULL DEFAULT 'zinc',
  created_at  TIMESTAMP NOT NULL,
  updated_at  TIMESTAMP NOT NULL,
  CHECK (color IN ('zinc','red','orange','amber','green','teal','blue','violet','pink'))
);

CREATE TABLE task_tags (
  task_id    TEXT NOT NULL,
  tag_slug   TEXT NOT NULL,
  sort_order INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, tag_slug),
  FOREIGN KEY (task_id)  REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (tag_slug) REFERENCES tags(slug) ON DELETE CASCADE
);

CREATE INDEX idx_task_tags_tag_slug ON task_tags(tag_slug);

-- Drop the legacy column. SQLite 3.35+ supports this.
ALTER TABLE tasks DROP COLUMN tags;
