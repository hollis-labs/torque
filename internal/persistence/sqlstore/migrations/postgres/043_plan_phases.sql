-- 043 plan_phases (Postgres override; same tables, columns and rules): see the
-- SQLite file for the design.
--
-- tasks.metadata is TEXT and may hold text that is not JSON, so the backfill
-- reads it through torque_safe_jsonb, which returns NULL instead of failing the
-- whole migration on one bad row.

CREATE OR REPLACE FUNCTION torque_safe_jsonb(src TEXT) RETURNS JSONB AS $$
BEGIN
    IF src IS NULL OR src = '' THEN
        RETURN NULL;
    END IF;
    RETURN src::jsonb;
EXCEPTION WHEN others THEN
    RETURN NULL;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE TABLE IF NOT EXISTS plan_phases (
    plan_id              TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    phase_id             TEXT NOT NULL,
    name                 TEXT NOT NULL DEFAULT '',
    display_order        INTEGER NOT NULL DEFAULT 0,
    acceptance           TEXT NOT NULL DEFAULT '',
    state                TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'started', 'completed')),
    lifecycle_generation INTEGER NOT NULL DEFAULT 1,
    started_at           TEXT,
    completed_at         TEXT,
    revision             INTEGER NOT NULL DEFAULT 1,
    deleted_at           TEXT,
    created_at           TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at           TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (plan_id, phase_id)
);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS phase_id TEXT;
CREATE INDEX IF NOT EXISTS idx_tasks_parent_phase ON tasks(parent_id, phase_id) WHERE phase_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS plan_phase_repairs (
    task_id    TEXT NOT NULL PRIMARY KEY,
    plan_id    TEXT,
    phase_id   TEXT NOT NULL,
    reason     TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD HH24:MI:SS')
);

INSERT INTO plan_phases (plan_id, phase_id, name, display_order, acceptance)
SELECT t.id,
       p.value ->> 'id',
       COALESCE(p.value ->> 'name', ''),
       CASE WHEN (p.value ->> 'order') ~ '^-?[0-9]+$' THEN (p.value ->> 'order')::integer ELSE 0 END,
       COALESCE(p.value ->> 'acceptance', '')
  FROM tasks t
  CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(torque_safe_jsonb(t.metadata) #> '{plan,phases}') = 'array'
             THEN torque_safe_jsonb(t.metadata) #> '{plan,phases}'
             ELSE '[]'::jsonb END) AS p(value)
 WHERE t.kind = 'plan'
   AND jsonb_typeof(p.value) = 'object'
   AND COALESCE(p.value ->> 'id', '') <> ''
ON CONFLICT (plan_id, phase_id) DO NOTHING;

UPDATE tasks
   SET phase_id = torque_safe_jsonb(metadata) ->> 'phase_id'
 WHERE parent_id IS NOT NULL
   AND torque_safe_jsonb(metadata) ->> 'phase_id' IS NOT NULL
   AND EXISTS (SELECT 1 FROM plan_phases pp
                WHERE pp.plan_id = tasks.parent_id
                  AND pp.phase_id = torque_safe_jsonb(tasks.metadata) ->> 'phase_id');

INSERT INTO plan_phase_repairs (task_id, plan_id, phase_id, reason)
SELECT id, parent_id, torque_safe_jsonb(metadata) ->> 'phase_id',
       CASE WHEN parent_id IS NULL THEN 'task has metadata.phase_id but no parent plan'
            ELSE 'parent has no such phase' END
  FROM tasks
 WHERE phase_id IS NULL
   AND COALESCE(torque_safe_jsonb(metadata) ->> 'phase_id', '') <> ''
ON CONFLICT (task_id) DO NOTHING;

UPDATE plan_phases
   SET state = 'completed'
 WHERE EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id)
   AND NOT EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id AND c.status <> 'done');

UPDATE plan_phases
   SET state = 'started'
 WHERE state = 'pending'
   AND EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id
                 AND c.status IN ('doing', 'review', 'blocked', 'paused', 'done'));

CREATE OR REPLACE FUNCTION torque_check_task_phase() RETURNS trigger AS $$
BEGIN
    IF NEW.phase_id IS NOT NULL AND (
        NEW.parent_id IS NULL
        OR NOT EXISTS (SELECT 1 FROM plan_phases
                        WHERE plan_id = NEW.parent_id AND phase_id = NEW.phase_id AND deleted_at IS NULL)
    ) THEN
        RAISE EXCEPTION 'invalid phase: phase_id must name a live phase of the parent plan';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tasks_phase_insert ON tasks;
CREATE TRIGGER trg_tasks_phase_insert BEFORE INSERT ON tasks
    FOR EACH ROW WHEN (NEW.phase_id IS NOT NULL) EXECUTE FUNCTION torque_check_task_phase();

DROP TRIGGER IF EXISTS trg_tasks_phase_update ON tasks;
CREATE TRIGGER trg_tasks_phase_update BEFORE UPDATE OF phase_id, parent_id ON tasks
    FOR EACH ROW WHEN (NEW.phase_id IS NOT NULL) EXECUTE FUNCTION torque_check_task_phase();

CREATE OR REPLACE FUNCTION torque_detach_plan_children() RETURNS trigger AS $$
BEGIN
    UPDATE tasks SET phase_id = NULL WHERE parent_id = OLD.id AND phase_id IS NOT NULL;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tasks_plan_delete ON tasks;
CREATE TRIGGER trg_tasks_plan_delete BEFORE DELETE ON tasks
    FOR EACH ROW WHEN (OLD.kind = 'plan') EXECUTE FUNCTION torque_detach_plan_children();
