# PRIM-004 — Archive primitive (`archived_at` + archive/unarchive)

**Phase:** 2 — Shared primitives
**Status:** done
**Depends on:** none
**Blocks:** ENT-PROJECT, ENT-EPIC, ENT-SPRINT
**Source:** ADR-0004 §3 (Archive); audit "Cross-Cutting Findings §5
(Convenience tools)"

## Summary

Archive as a concept independent of `status` — archiving an epic isn't the
same fact as the epic being "done." Target: a real nullable `archived_at`
column on Project, Epic, Sprint, Issue, Plan, with `archive`/`unarchive`
tools, kept orthogonal to `status`. Collection already has the clean
reference pattern (`archive`/`unarchive` pair) to follow.

## Scope

- Migration: add nullable `archived_at` to Project, Epic, Sprint, and — per
  the ADR's target list — Issue and Plan (both are rows in `tasks`, so
  confirm whether this means a shared `tasks.archived_at` column or a
  narrower kind-scoped approach; Task itself explicitly keeps hard-delete +
  `abandoned` status as its own audit-preserving close path, archive is
  "optional parity there, not a gap" — don't force archive onto plain Task
  rows as a requirement of this migration).
- Generic `archive`/`unarchive` service methods, following Collection's
  existing pattern (`CollectionService`) as the reference implementation —
  read it first before building a new pattern from scratch.
- List filters should default to excluding archived rows unless an
  `include_archived` param is passed (check what Collection/Template
  already do here for consistency — Template already has an
  `IncludeArchived` filter param to mirror).

## Acceptance criteria

- [x] `archived_at` migration lands for Project, Epic, Sprint (Issue/Plan
      scope confirmed per the note above before implementing).
- [x] Generic archive/unarchive service methods exist, matching
      Collection's pattern.
- [x] Archiving doesn't change `status` — verified by archiving a row and
      confirming its status field is untouched.
- [x] List tools exclude archived rows by default; `include_archived=true`
      (or equivalent) surfaces them.

## Out of scope

- Wiring `archive`/`unarchive` MCP tools per-entity — that's each entity's
  Phase 4 task (this task builds the shared migration + service methods
  only).
- Task's own archive parity — explicitly optional per ADR, not required
  here.

## Execution notes

**Scope decision (made by dispatcher, not this agent):** implemented
`archived_at` for Project, Epic, Sprint only, per the task's own
Acceptance Criteria. Issue/Plan scope was explicitly deferred — see "Open
question: Issue/Plan" below — and not implemented, so no changes touch the
shared `tasks` table's schema.

### Migration

- `internal/persistence/sqlstore/migrations/030_archived_at.sql`: adds
  nullable `archived_at DATETIME` to `projects`, `epics`, `sprints`
  (NULL = active, mirrors the `collections.archived_at` design from
  migration 020), plus one index per table
  (`idx_projects_archived`, `idx_epics_archived`, `idx_sprints_archived`).
  No backfill needed — new column, existing rows land NULL.
- Verified by `internal/persistence/sqlstore/migrations/migrate_test.go`
  (new assertions: columns exist, new rows default to NULL, indexes exist).

### Reference pattern followed

Read `CollectionService`/`Store.ArchiveCollection`/`UnarchiveCollection`
(`internal/service/collection.go`, `internal/persistence/sqlstore/collections.go`)
as the reference implementation, per the task's explicit instruction:
- Store: `Archive<Entity>(id)` sets `archived_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP`; `Unarchive<Entity>(id)` clears
  `archived_at = NULL`. Both are idempotent (re-archiving refreshes the
  timestamp) and return a not-found error via `RowsAffected() == 0`,
  matching Collection's style exactly (this codebase's Project/Epic/Sprint
  store methods use plain `fmt.Errorf(...)` rather than Collection's
  wrapped sentinel-error style, so the not-found errors here match the
  pre-existing Get/Update/Delete error style in each file rather than
  introducing a new `ErrXNotFound` sentinel that would be inconsistent
  with the rest of that file).
- Service: thin `Archive`/`Unarchive` wrappers gated on
  `feature.Require(...)`, exactly like `CollectionService.Archive/Unarchive`.
- List filter: for the `IncludeArchived` field/param name and "false
  excludes, true surfaces all" semantics, mirrored
  `TemplateService.List`/`Store.ListTemplates(includeArchived bool, ...)`
  (`internal/service/template.go`, `internal/persistence/sqlstore/templates.go`)
  rather than Collection's `status` enum ("active"/"archived"/"all") —
  Project/Epic/Sprint already have a real `status` field with unrelated
  workflow semantics (active/inactive/completed), so overloading `status`
  the way Collection does would collide with existing meaning. Each
  `<Entity>Filter` struct gained an `IncludeArchived bool` field
  (zero value = false = excludes archived rows via `archived_at IS NULL`),
  matching Template's `TemplateListOpts.IncludeArchived` bool exactly.

### Service methods added

- `ProjectService.Archive(id) error` / `.Unarchive(id) error`
  (`internal/service/project.go`)
- `EpicService.Archive(id) error` / `.Unarchive(id) error`
  (`internal/service/epic.go`)
- `SprintService.Archive(id) error` / `.Unarchive(id) error`
  (`internal/service/sprint.go`)
- Store-level: `ArchiveProject`/`UnarchiveProject`, `ArchiveEpic`/
  `UnarchiveEpic`, `ArchiveSprint`/`UnarchiveSprint`
  (`internal/persistence/sqlstore/{projects,epics,sprints}.go`)

### List signature change (compile-preserving, not new MCP wiring)

`ProjectService.List`, `EpicService.List`, `SprintService.List` each grew
a new `includeArchived bool` trailing parameter (store-level `ListProjects`
/`ListEpics`/`ListSprints` filter structs grew `IncludeArchived bool`).
This is a real signature change, so every existing call site had to be
updated to keep the build green — all pass `false` (preserve current
behavior; harmless today since no row can yet have `archived_at` set
through any wired tool):
- `internal/mcpadapter/{project_tools,epic_tools,sprint_tools}.go`
  (list handlers) — comment left in place noting that exposing
  `include_archived` in the tool schema is Phase 4's job, not this task's.
- `internal/httpserver/{projects,epics,sprints}.go` (list handlers)
- `internal/service/{project_test,epic_test,sprint_test}.go` (existing
  `.List(...)` calls updated to the new arity)

No MCP tool schema, HTTP query param, or new `archive`/`unarchive` tool
was wired up anywhere — that's explicitly Phase 4's job per this task's
Out of scope section. This task only makes the primitive available for
Phase 4 to consume.

### Status-independence verification

Added one test per entity (`TestEpicArchiveDoesNotChangeStatus`,
`TestProjectArchiveDoesNotChangeStatus`, `TestSprintArchiveDoesNotChangeStatus`
in the corresponding `internal/service/*_test.go`) that creates a row,
archives it, asserts `ArchivedAt.Valid == true` and `Status` unchanged,
then unarchives and asserts `ArchivedAt.Valid == false` and `Status` still
unchanged.

### Default-exclude list filter verification

Added one test per entity (`TestEpicListExcludesArchivedByDefault`,
`TestProjectListExcludesArchivedByDefault`, `TestSprintListExcludesArchivedByDefault`)
that creates two rows, archives one, asserts `List(..., false)` returns
only the unarchived row, and `List(..., true)` returns both.

### Open question: Issue/Plan (explicitly not implemented)

The task's own Summary/Scope text says the ADR's target list includes
Issue and Plan, both of which are rows in the shared `tasks` table, and
flags that this needs separate confirmation before implementing: does
"archive Issue/Plan" mean a shared `tasks.archived_at` column (which would
also affect Task rows, sharing the same schema), or a narrower kind-scoped
approach? This was deliberately deferred per the dispatching agent's
explicit scope decision and is **not implemented** by this change — no
`archived_at` column exists on `tasks`. This should be resolved (likely as
a DEC-xxx decision doc, given the shape mirrors DEC-001/002/003) before
ENT-ISSUE / ENT-PLAN (Phase 4) need archive support. Task itself is
correctly out of scope per the ticket (hard-delete + `abandoned` status is
its own audit-preserving close path; archive parity there is optional, not
required).

### Files touched

- `internal/persistence/sqlstore/migrations/030_archived_at.sql` (new)
- `internal/persistence/sqlstore/migrations/migrate_test.go`
- `internal/persistence/sqlstore/{projects,epics,sprints}.go`
- `internal/service/{project,epic,sprint}.go`
- `internal/service/{project_test,epic_test,sprint_test}.go`
- `internal/mcpadapter/{project_tools,epic_tools,sprint_tools}.go` (call-site arity fix only)
- `internal/httpserver/{projects,epics,sprints}.go` (call-site arity fix only)
- `tasks/phase-2-primitives/PRIM-004-archive-primitive.md` (this file)

### Verification

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./...` — all packages pass, including the 6 new archive/list
  tests and the extended migration test.

### Issues / blockers

None. No overlap observed with the parallel Task-primitive work
(`internal/mcpadapter/task_tools.go`, `internal/service/task.go` were not
touched).
