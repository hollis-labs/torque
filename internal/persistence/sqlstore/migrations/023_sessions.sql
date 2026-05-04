-- 023 long-lived agent session manager (CW-20260503-0014, S1.4).
--
-- The sessionmgr package owns lifecycle (launch/attach/stop/wait/resize/
-- checkpoint/resume) for agent sessions that outlive a single task. State is
-- persisted on every transition so a daemon crash leaves a recoverable
-- record; the orphan sweep on startup reconciles `running` rows whose PID is
-- gone (mirrors mux's SweepStaleSessions pattern).
--
-- Schema is re-designed for Clockwork — does NOT import mux's columns. The
-- four-value state set matches go-agent-sessions (launching|running|done|
-- failed); a fifth `crashed` state is reserved for orphan-sweep transitions
-- so dashboards can distinguish a clean exit from a daemon-killed survivor.
--
-- Both tables are forward-only (additive); no existing rows to backfill.

CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    -- agent_profile is the clockwork agent profile name (e.g. "default",
    -- "reviewer-end-agent"). Resolved at launch via config.ProfileMap.
    agent_profile   TEXT NOT NULL DEFAULT '',
    -- provider mirrors profile.Provider (claude|codex|gemini|copilot|opencode)
    -- so an orphan-sweep / list query can group by provider without joining
    -- a profile registry that may have changed since launch.
    provider        TEXT NOT NULL DEFAULT '',
    -- runtime_id / runtime_kind are the go-agent-sessions Runtime descriptors
    -- (e.g. "clockwork-cli/claude", "cli"). Stored for diagnostic surfacing.
    runtime_id      TEXT NOT NULL DEFAULT '',
    runtime_kind    TEXT NOT NULL DEFAULT '',
    -- workdir is the spawned process's cwd (boot dir under the cliexec
    -- pattern; not the project root).
    workdir         TEXT NOT NULL DEFAULT '',
    -- project_id is optional FK soft-link; sessionmgr does not enforce.
    project_id      TEXT,
    -- task_id is the task this session is bound to, when applicable. NULL
    -- for sessions launched outside a single-task scope (orchestrator,
    -- standing reviewer end-agents). Soft FK only.
    task_id         TEXT,
    state           TEXT NOT NULL DEFAULT 'launching'
      CHECK (state IN ('launching','running','done','failed','crashed')),
    pid             INTEGER NOT NULL DEFAULT 0,
    exit_code       INTEGER,
    -- resume_hint is the opaque CheckpointHint bytes from the most recent
    -- checkpoint emit (when the underlying adapter supports it). NULL when
    -- the adapter has none. Not the same as session_checkpoints.payload —
    -- this is the structural resume token; payload is consumer JSON.
    resume_hint     BLOB,
    -- meta is opaque JSON for caller-supplied SessionMeta.
    meta            TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_activity   DATETIME DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME
);

CREATE INDEX IF NOT EXISTS idx_sessions_state         ON sessions(state);
CREATE INDEX IF NOT EXISTS idx_sessions_task_id       ON sessions(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_project_id    ON sessions(project_id) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_last_activity ON sessions(last_activity);

-- session_checkpoints: agent-driven cross-session continuity records. NOT
-- the same shape as the existing `checkpoints` table (migration 008), which
-- is the human-in-the-loop checkpoint type with response semantics. Naming
-- the table `session_checkpoints` keeps the two namespaces separate —
-- sessionmgr emits/replays its own rows without grafting onto the HITL
-- vocabulary.
CREATE TABLE IF NOT EXISTS session_checkpoints (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    -- payload is opaque JSON the consumer hands the manager — the shape
    -- belongs to go-envelope (deferred); sessionmgr only persists.
    payload         TEXT NOT NULL DEFAULT '{}',
    -- resume_hint is the go-agent-sessions CheckpointHint snapshot at emit
    -- time. NULL for adapters with no hint.
    resume_hint     BLOB,
    note            TEXT NOT NULL DEFAULT '',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_session_checkpoints_session
    ON session_checkpoints(session_id, created_at);
