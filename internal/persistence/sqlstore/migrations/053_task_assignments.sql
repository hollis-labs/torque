-- G6 (CW-20261121-0148): who holds a task, and whether that holder is gone.
-- One row per task: the current or last assignment. Expiry keeps the row with
-- state='expired' and the stale holder, so Ops can tell "expired claim" from
-- "assigned, holder not live". Field names are the contract Surface reads
-- (surface#68): the typed task record's top-level `assignment` object.
CREATE TABLE task_assignments (
    task_id           TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    assignee_urn      TEXT NOT NULL,
    assignee_name     TEXT NOT NULL DEFAULT '',
    tether_session_id TEXT NOT NULL DEFAULT '',
    team_run_id       TEXT NOT NULL DEFAULT '',
    claim_id          TEXT NOT NULL DEFAULT '',
    source            TEXT NOT NULL CHECK (source IN ('owner','doing','claim','dispatch')),
    state             TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','expired','released')),
    assigned_at       TEXT NOT NULL,
    lease_expires_at  TEXT NOT NULL DEFAULT '',
    ended_at          TEXT NOT NULL DEFAULT '',
    end_reason        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX task_assignments_active ON task_assignments(state) WHERE state='active';
