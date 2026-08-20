-- 027 fix epics.status DEFAULT from 'open' to 'active' (FIX-003).
--
-- 001_initial.sql set epics.status DEFAULT 'open', but every real code path
-- disagrees: EpicService.Create (internal/service/epic.go) and
-- Store.CreateEpic (internal/persistence/sqlstore/epics.go) both explicitly
-- write status="active" on insert, EpicService.Update's validEpicStatuses
-- only accepts {active, inactive}, and the GUI's ContainerStatus type /
-- epic edit form only offer active|inactive (mirroring projects.status,
-- whose default was already corrected to 'active' back in 004). The
-- torque_epic_update/list MCP tool docstrings advertised open/closed
-- instead, matching nothing — fixed in the same change as this migration.
--
-- No code path has ever produced status='open' for an epic (CreateEpic
-- always overrides an empty status to 'active' before insert), so there is
-- no existing data to backfill — this migration only changes the default
-- applied to future inserts that bypass CreateEpic's Go-level default.
--
-- SQLite cannot ALTER a column's DEFAULT; full table rebuild required.
-- Column list reproduces 001_initial.sql's epics table plus the priority
-- and project_id columns 004_enrich_entities.sql added via ALTER TABLE; no
-- other migration has touched epics.

CREATE TABLE epics_new (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active',
    priority        INTEGER,
    project_id      TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO epics_new
SELECT id, name, description, status, priority, project_id, created_at, updated_at
FROM epics;

DROP TABLE epics;
ALTER TABLE epics_new RENAME TO epics;
