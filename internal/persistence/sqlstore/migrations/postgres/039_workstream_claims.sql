-- 039 workstream claims (Postgres override; same shape, TIMESTAMPTZ).
-- A claim is the lease a team run holds on a workstream (a parent task with
-- children). team_run_id is the Tether run id, not the team definition id.
-- A claim is keyed by (team_run_id, idempotency_key) so a retry returns the same row.
CREATE TABLE IF NOT EXISTS workstream_claims (
    id               TEXT PRIMARY KEY,
    task_id          TEXT NOT NULL,
    project_id       TEXT NOT NULL DEFAULT '',
    team_id          TEXT NOT NULL DEFAULT '',
    team_run_id      TEXT NOT NULL,
    actor            TEXT NOT NULL DEFAULT '',
    environment_id   TEXT NOT NULL DEFAULT '',
    idempotency_key  TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'active',
    outcome          TEXT NOT NULL DEFAULT '',
    result_refs      TEXT,
    lease_expires_at TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    released_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workstream_claims_one_active_per_task
    ON workstream_claims(task_id) WHERE status = 'active';
CREATE UNIQUE INDEX IF NOT EXISTS idx_workstream_claims_idem
    ON workstream_claims(team_run_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_workstream_claims_project_active
    ON workstream_claims(project_id) WHERE status = 'active';
