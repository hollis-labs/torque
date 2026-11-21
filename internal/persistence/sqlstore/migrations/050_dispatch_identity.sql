-- Registry is the identity authority. This is a durable retry/binding ledger,
-- never a local workstream mint. Old keys remain so outage-era events cannot
-- accidentally inherit a task's later workstream.
CREATE TABLE dispatch_workstreams (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    logical_key TEXT NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    canonical_id TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending',
    active INTEGER NOT NULL DEFAULT 1,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(task_id,logical_key,project_id)
);
CREATE UNIQUE INDEX dispatch_workstream_active ON dispatch_workstreams(task_id) WHERE active=1;
CREATE TABLE dispatch_event_workstreams (
    event_id TEXT PRIMARY KEY REFERENCES event_outbox(event_id) ON DELETE CASCADE,
    task_id TEXT NOT NULL,
    logical_key TEXT NOT NULL,
    project_id TEXT NOT NULL,
    unit_run_id TEXT NOT NULL DEFAULT '',
    is_unit INTEGER NOT NULL DEFAULT 0
);

-- Seal the exact outbound JSON before HTTP: a replay remains identical even
-- when a task title, active run or binding changes while the response is lost.
CREATE TABLE dispatch_status_payloads (
    event_id TEXT PRIMARY KEY REFERENCES event_outbox(event_id) ON DELETE CASCADE,
    payload TEXT NOT NULL
);
