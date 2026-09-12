-- 032 add a Torque-owned terminal "canceled" state for sessions.
--
-- go-agent-sessions reports done/failed/crashed-style process outcomes, but
-- Torque also needs to distinguish an intentional operator pause that killed
-- the process from an agent failure. SQLite cannot ALTER a CHECK constraint,
-- so rebuild sessions with the same columns and indexes, widening only the
-- state vocabulary.

CREATE TABLE sessions_new (
    id              TEXT PRIMARY KEY,
    agent_profile   TEXT NOT NULL DEFAULT '',
    provider        TEXT NOT NULL DEFAULT '',
    runtime_id      TEXT NOT NULL DEFAULT '',
    runtime_kind    TEXT NOT NULL DEFAULT '',
    workdir         TEXT NOT NULL DEFAULT '',
    project_id      TEXT,
    task_id         TEXT,
    state           TEXT NOT NULL DEFAULT 'launching'
      CHECK (state IN ('launching','running','done','failed','canceled','crashed')),
    pid             INTEGER NOT NULL DEFAULT 0,
    exit_code       INTEGER,
    resume_hint     BLOB,
    meta            TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_activity   DATETIME DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME,
    launch_profile  TEXT NOT NULL DEFAULT ''
);

INSERT INTO sessions_new (
    id, agent_profile, provider, runtime_id, runtime_kind,
    workdir, project_id, task_id, state, pid, exit_code, resume_hint, meta,
    created_at, updated_at, last_activity, ended_at, launch_profile
)
SELECT
    id, agent_profile, provider, runtime_id, runtime_kind,
    workdir, project_id, task_id, state, pid, exit_code, resume_hint, meta,
    created_at, updated_at, last_activity, ended_at, launch_profile
FROM sessions;

DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX IF NOT EXISTS idx_sessions_state         ON sessions(state);
CREATE INDEX IF NOT EXISTS idx_sessions_task_id       ON sessions(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_project_id    ON sessions(project_id) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_last_activity ON sessions(last_activity);
