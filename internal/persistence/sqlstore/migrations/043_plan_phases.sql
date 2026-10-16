-- 043 plan_phases: plan phases become records (CW-20261016-0134, T2).
--
-- Until now a phase was an element of the plan task's metadata.plan.phases and
-- a child joined it through metadata.phase_id: no state, no lifecycle times,
-- and a deleted phase's id could be handed out again. A phase is now a row.
--
--   * (plan_id, phase_id) is the key. phase_id keeps the "ph-N" shape.
--   * A removed phase is tombstoned (deleted_at), never deleted, so its id is
--     not reused: new ids are allocated above every suffix ever issued.
--   * state is pending | started | completed. started_at / completed_at are
--     set only when the transition happens; the backfill leaves them NULL
--     rather than invent a history.
--   * lifecycle_generation starts at 1 and is bumped by an explicit reopen of
--     a completed phase; revision is the optimistic-concurrency counter.
--   * tasks.phase_id is validated against the task's parent plan by triggers,
--     so every write path (Store, WriteTx, raw SQL) is held to it.
--
-- Additive: no down migration. Rollback is a code revert; the unused table and
-- column are inert, and metadata.plan / metadata.phase_id are left untouched.

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
    created_at           TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plan_id, phase_id)
);

ALTER TABLE tasks ADD COLUMN phase_id TEXT;
CREATE INDEX IF NOT EXISTS idx_tasks_parent_phase ON tasks(parent_id, phase_id) WHERE phase_id IS NOT NULL;

-- Things the backfill could not turn into records, listed here for repair
-- instead of failing the migration or inventing data: a child that names a phase
-- its plan does not have, and a plan whose metadata.plan.phases is not an array
-- or holds an entry that is not an object with a text id (task_id is then the
-- plan, phase_id empty).
CREATE TABLE IF NOT EXISTS plan_phase_repairs (
    task_id    TEXT NOT NULL,
    plan_id    TEXT,
    phase_id   TEXT NOT NULL,
    reason     TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, reason)
);

-- One-time backfill from metadata.plan.phases. A phase listed twice keeps its
-- first row; a phase with no id is skipped.
INSERT OR IGNORE INTO plan_phases (plan_id, phase_id, name, display_order, acceptance)
SELECT t.id,
       json_extract(p.value, '$.id'),
       COALESCE(json_extract(p.value, '$.name'), ''),
       COALESCE(json_extract(p.value, '$.order'), 0),
       COALESCE(json_extract(p.value, '$.acceptance'), '')
  FROM tasks t,
       json_each(CASE WHEN t.kind = 'plan' AND json_valid(t.metadata)
                       AND json_type(t.metadata, '$.plan.phases') = 'array'
                      THEN json_extract(t.metadata, '$.plan.phases') END) p
 WHERE p.type = 'object'
   AND COALESCE(CASE WHEN p.type = 'object' THEN json_type(p.value, '$.id') = 'text' END, 0)
   AND COALESCE(CASE WHEN p.type = 'object' THEN json_extract(p.value, '$.id') END, '') <> '';

-- A plan whose phases value is not an array.
INSERT OR IGNORE INTO plan_phase_repairs (task_id, plan_id, phase_id, reason)
SELECT t.id, t.id, '', 'metadata.plan.phases is not an array'
  FROM tasks t
 WHERE t.kind = 'plan' AND json_valid(t.metadata)
   AND json_type(t.metadata, '$.plan.phases') IS NOT NULL
   AND json_type(t.metadata, '$.plan.phases') NOT IN ('array', 'null');

-- A plan with an entry that is not an object with a non-empty text id.
INSERT OR IGNORE INTO plan_phase_repairs (task_id, plan_id, phase_id, reason)
SELECT t.id, t.id, '', 'a metadata.plan.phases entry is not an object with a text id and was skipped'
  FROM tasks t,
       json_each(CASE WHEN t.kind = 'plan' AND json_valid(t.metadata)
                       AND json_type(t.metadata, '$.plan.phases') = 'array'
                      THEN json_extract(t.metadata, '$.plan.phases') END) p
 WHERE NOT COALESCE(
         CASE WHEN p.type = 'object'
              THEN json_type(p.value, '$.id') = 'text' AND json_extract(p.value, '$.id') <> ''
         END, 0);

-- Memberships: a child of a plan whose metadata.phase_id names one of its phases.
UPDATE tasks
   SET phase_id = json_extract(metadata, '$.phase_id')
 WHERE parent_id IS NOT NULL
   AND json_valid(metadata)
   AND json_extract(metadata, '$.phase_id') IS NOT NULL
   AND EXISTS (SELECT 1 FROM plan_phases pp
                WHERE pp.plan_id = tasks.parent_id
                  AND pp.phase_id = json_extract(tasks.metadata, '$.phase_id'));

INSERT OR IGNORE INTO plan_phase_repairs (task_id, plan_id, phase_id, reason)
SELECT id, parent_id, json_extract(metadata, '$.phase_id'),
       CASE WHEN parent_id IS NULL THEN 'task has metadata.phase_id but no parent plan'
            ELSE 'parent has no such phase' END
  FROM tasks
 WHERE phase_id IS NULL
   AND json_valid(metadata)
   AND json_extract(metadata, '$.phase_id') IS NOT NULL
   AND json_extract(metadata, '$.phase_id') <> '';

-- Current state from the verified child rows. All children done is completed;
-- any child past todo/queued/backlog means started. No timestamps are set.
UPDATE plan_phases
   SET state = 'completed'
 WHERE EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id)
   AND NOT EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id AND c.status <> 'done');

UPDATE plan_phases
   SET state = 'started'
 WHERE state = 'pending'
   AND EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = plan_phases.plan_id AND c.phase_id = plan_phases.phase_id
                 AND c.status IN ('doing', 'review', 'blocked', 'paused', 'done'));

-- phase_id must name a live phase of the task's own parent plan.
CREATE TRIGGER IF NOT EXISTS trg_tasks_phase_insert
BEFORE INSERT ON tasks
WHEN NEW.phase_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'invalid phase: phase_id must name a live phase of the parent plan')
     WHERE NEW.parent_id IS NULL
        OR NOT EXISTS (SELECT 1 FROM plan_phases
                        WHERE plan_id = NEW.parent_id AND phase_id = NEW.phase_id AND deleted_at IS NULL);
END;

CREATE TRIGGER IF NOT EXISTS trg_tasks_phase_update
BEFORE UPDATE OF phase_id, parent_id ON tasks
WHEN NEW.phase_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'invalid phase: phase_id must name a live phase of the parent plan')
     WHERE NEW.parent_id IS NULL
        OR NOT EXISTS (SELECT 1 FROM plan_phases
                        WHERE plan_id = NEW.parent_id AND phase_id = NEW.phase_id AND deleted_at IS NULL);
END;

-- A deleted plan takes its phases with it (ON DELETE CASCADE); detach the
-- children first so none is left holding a phase_id with no phase behind it.
CREATE TRIGGER IF NOT EXISTS trg_tasks_plan_delete
BEFORE DELETE ON tasks
WHEN OLD.kind = 'plan'
BEGIN
    UPDATE tasks SET phase_id = NULL WHERE parent_id = OLD.id AND phase_id IS NOT NULL;
END;
