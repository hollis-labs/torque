-- 009 task_templates — spec 2026-04-16-task-model-mvp-design.md §5.1.
-- Versioned per (id, version) — updates append new rows; archives are
-- soft-removals (is_archived=1) so historical references survive.
-- JSON blobs (tools, permissions, environment, escalation_chain,
-- quality_gates, deliverables, metadata_template, required_vars, tags)
-- are TEXT storing JSON documents validated at the service layer.

CREATE TABLE task_templates (
    id                     TEXT NOT NULL,
    version                INTEGER NOT NULL DEFAULT 1,
    name                   TEXT NOT NULL,
    description            TEXT NOT NULL,
    kind                   TEXT NOT NULL,
    auto_execute           BOOLEAN NOT NULL DEFAULT 1,
    executor               TEXT,
    agent_profile          TEXT,
    system_prompt          TEXT,
    tools                  TEXT,
    permissions            TEXT,
    environment            TEXT,
    cost_budget            REAL,
    max_retries            INTEGER DEFAULT 3,
    max_duration_ms        INTEGER,
    token_budget           INTEGER,
    on_done                TEXT NOT NULL DEFAULT 'review',
    on_fail                TEXT NOT NULL DEFAULT 'retry',
    on_review              TEXT NOT NULL DEFAULT 'pause',
    on_done_merge          TEXT NOT NULL DEFAULT 'none',
    escalation_chain       TEXT,
    quality_gates          TEXT,
    deliverables           TEXT,
    checkpoint_mode        TEXT NOT NULL DEFAULT 'none',
    on_checkpoint_response TEXT NOT NULL DEFAULT 'resume',
    metadata_template      TEXT,
    required_vars          TEXT,
    tags                   TEXT,
    is_archived            BOOLEAN NOT NULL DEFAULT 0,
    created_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (id, version)
);

CREATE INDEX idx_templates_kind ON task_templates(kind) WHERE is_archived = 0;
