-- 040 planner: the portfolio planner, ported from ~/dev/projects/portfolio-tracker.
-- Feature-flagged behind features.planner.
--
-- Design:
--   * One typed table per database. Scalars are columns; arrays and objects are
--     JSON text. `extra` holds any field the schema does not name, so an import
--     from the JSON files loses nothing.
--   * Ids keep the tracker prefixes (PR-, WS-, RM-, ID-, DEC-, RK-, IN-) and are
--     never reused; there is no delete, a dropped item gets a status.
--   * created/updated stay TEXT: the source carries plain dates, not instants.
--   * `ord` is the manual ordering (`order` is reserved in SQL); `rev` is the
--     optimistic-concurrency counter, 1 on create.
--   * Comments and external links are shared tables keyed by (db, item_id).
--     Cross-references between items stay as JSON id arrays on the item.

CREATE TABLE IF NOT EXISTS planner_priorities (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    rank INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'queued',
    summary TEXT NOT NULL DEFAULT '',
    sub_items TEXT NOT NULL DEFAULT '[]',
    open_questions TEXT NOT NULL DEFAULT '[]',
    roadmap_ids TEXT NOT NULL DEFAULT '[]',
    workstream_ids TEXT NOT NULL DEFAULT '[]',
    idea_ids TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_planner_priorities_status ON planner_priorities(status);

CREATE TABLE IF NOT EXISTS planner_workstreams (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'planned',
    phase TEXT NOT NULL DEFAULT '',
    tether_workstream_id TEXT NOT NULL DEFAULT '',
    torque_project_ids TEXT NOT NULL DEFAULT '[]',
    torque_tag TEXT NOT NULL DEFAULT '',
    owner TEXT NOT NULL DEFAULT '',
    goal TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    next_step TEXT NOT NULL DEFAULT '',
    blockers TEXT NOT NULL DEFAULT '[]',
    priority_ids TEXT NOT NULL DEFAULT '[]',
    roadmap_ids TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_planner_workstreams_status ON planner_workstreams(status);

CREATE TABLE IF NOT EXISTS planner_roadmap (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    area TEXT NOT NULL DEFAULT '',
    horizon TEXT NOT NULL DEFAULT 'someday',
    status TEXT NOT NULL DEFAULT 'idea',
    mvp INTEGER NOT NULL DEFAULT 0,
    outcome TEXT NOT NULL DEFAULT '',
    depends_on TEXT NOT NULL DEFAULT '[]',
    priority_ids TEXT NOT NULL DEFAULT '[]',
    workstream_ids TEXT NOT NULL DEFAULT '[]',
    tasks TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_planner_roadmap_status ON planner_roadmap(status);
CREATE INDEX IF NOT EXISTS idx_planner_roadmap_horizon ON planner_roadmap(horizon);
CREATE INDEX IF NOT EXISTS idx_planner_roadmap_area ON planner_roadmap(area);

CREATE TABLE IF NOT EXISTS planner_ideas (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    kind TEXT NOT NULL DEFAULT 'idea',
    status TEXT NOT NULL DEFAULT 'new',
    summary TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT '',
    options TEXT NOT NULL DEFAULT '[]',
    recommendation TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '',
    discuss_in TEXT NOT NULL DEFAULT '',
    priority_ids TEXT NOT NULL DEFAULT '[]',
    roadmap_ids TEXT NOT NULL DEFAULT '[]',
    workstream_ids TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_planner_ideas_status ON planner_ideas(status);
CREATE INDEX IF NOT EXISTS idx_planner_ideas_kind ON planner_ideas(kind);

CREATE TABLE IF NOT EXISTS planner_decisions (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'needs-decision',
    date TEXT NOT NULL DEFAULT '',
    decided_by TEXT NOT NULL DEFAULT '',
    area TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '',
    rationale TEXT NOT NULL DEFAULT '',
    alternatives TEXT NOT NULL DEFAULT '[]',
    supersedes TEXT NOT NULL DEFAULT '',
    superseded_by TEXT NOT NULL DEFAULT '',
    options TEXT NOT NULL DEFAULT '[]',
    selected_option TEXT NOT NULL DEFAULT '',
    decided_at TEXT NOT NULL DEFAULT '',
    defer_note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_planner_decisions_status ON planner_decisions(status);

CREATE TABLE IF NOT EXISTS planner_risks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    kind TEXT NOT NULL DEFAULT 'risk',
    severity TEXT NOT NULL DEFAULT 'medium',
    status TEXT NOT NULL DEFAULT 'open',
    description TEXT NOT NULL DEFAULT '',
    impact TEXT NOT NULL DEFAULT '',
    mitigation TEXT NOT NULL DEFAULT '',
    owner TEXT NOT NULL DEFAULT '',
    raised TEXT NOT NULL DEFAULT '',
    roadmap_ids TEXT NOT NULL DEFAULT '[]',
    workstream_ids TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_planner_risks_status ON planner_risks(status);
CREATE INDEX IF NOT EXISTS idx_planner_risks_severity ON planner_risks(severity);

CREATE TABLE IF NOT EXISTS planner_inbox (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    tldr        TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '[]',
    related_ids TEXT NOT NULL DEFAULT '[]',
    author      TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    updated     TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    ord         REAL,
    priority    INTEGER,
    extra       TEXT NOT NULL DEFAULT '{}',
    kind TEXT NOT NULL DEFAULT 'topic',
    status TEXT NOT NULL DEFAULT 'new',
    body TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    added_by TEXT NOT NULL DEFAULT '',
    promoted_to TEXT NOT NULL DEFAULT 'null'
);
CREATE INDEX IF NOT EXISTS idx_planner_inbox_status ON planner_inbox(status);

CREATE TABLE IF NOT EXISTS planner_comments (
    pk       INTEGER PRIMARY KEY AUTOINCREMENT,
    db       TEXT NOT NULL,            -- priorities | workstreams | roadmap | ideas | decisions | risks | inbox
    item_id  TEXT NOT NULL,
    cid      TEXT NOT NULL DEFAULT '', -- the comment id carried from the source data
    author   TEXT NOT NULL DEFAULT '',
    kind     TEXT NOT NULL DEFAULT 'comment',
    text     TEXT NOT NULL,
    created  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_planner_comments_item ON planner_comments(db, item_id, pk);

CREATE TABLE IF NOT EXISTS planner_links (
    pk      INTEGER PRIMARY KEY AUTOINCREMENT,
    db      TEXT NOT NULL,
    item_id TEXT NOT NULL,
    kind    TEXT NOT NULL,             -- torque | tesseract | tether | pr | adr | url | file | commit | workstream
    ref     TEXT NOT NULL,
    label   TEXT NOT NULL DEFAULT '',
    UNIQUE (db, item_id, kind, ref)
);
