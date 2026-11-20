-- Requests are manual Torque tasks with a separate transport lifecycle.
CREATE TABLE task_requests (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    mail_message_id TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL,
    due_ack_at TEXT NOT NULL,
    awaiting_ack INTEGER NOT NULL DEFAULT 1,
    record TEXT NOT NULL
);
CREATE INDEX task_requests_ack_due ON task_requests(awaiting_ack, due_ack_at, task_id);
