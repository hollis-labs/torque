-- 027_task_dependencies.sql
-- Normalize tasks.depends_on into a real join table (FK-003 / DEC-003).
--
-- The legacy `depends_on` column is a JSON array of task IDs in a single
-- TEXT column. Existence was checked only at write time; nothing ever
-- pruned a stale ID when the referenced task was deleted later, so a task
-- whose dependency was deleted became permanently, silently unschedulable
-- (internal/runtime/scheduler/picker.go's dependency gate treats a lookup
-- failure as "not met" forever, recorded as SkipReasonDepUnmet). Promoting
-- to a join table with ON DELETE CASCADE on depends_on_task_id fixes this:
-- deleting the dependency task automatically removes the edge, so the
-- dependent task is no longer blocked on a phantom dependency on the next
-- scheduling tick. ON DELETE CASCADE on task_id prevents orphaned join rows
-- when the dependent task itself is deleted (a second, smaller
-- dangling-reference direction).
--
-- Mirrors migrations/005_tags.sql's tags -> task_tags promotion.

CREATE TABLE task_dependencies (
  task_id            TEXT NOT NULL,
  depends_on_task_id TEXT NOT NULL,
  sort_order         INTEGER NOT NULL,
  created_at         TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, depends_on_task_id),
  FOREIGN KEY (task_id)            REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (depends_on_task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX idx_task_dependencies_depends_on_task_id ON task_dependencies(depends_on_task_id);

-- Backfill from the legacy JSON column so existing dependency edges survive
-- the promotion. Entries pointing at a task ID that no longer exists are
-- intentionally dropped here rather than carried forward — those are
-- exactly the dangling references this migration exists to stop producing,
-- and the depends_on_task_id FK would reject them anyway.
INSERT INTO task_dependencies (task_id, depends_on_task_id, sort_order, created_at)
SELECT t.id, je.value, je.key, CURRENT_TIMESTAMP
FROM (
  SELECT id, depends_on FROM tasks
  WHERE depends_on IS NOT NULL AND depends_on != '' AND depends_on != '[]'
) t, json_each(t.depends_on) je
WHERE je.value IN (SELECT id FROM tasks);

-- Drop the legacy column. SQLite 3.35+ supports this.
ALTER TABLE tasks DROP COLUMN depends_on;
