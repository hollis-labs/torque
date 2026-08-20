# ENT-SPRINT — Sprint: list sort/cursor, archive, bulk_update, budget filter

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** FIX-004, PRIM-001, PRIM-002, PRIM-003, PRIM-004, DEC-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Sprint); audit "Per-Entity Findings → Sprint"

## Summary

Sprint target inventory: `create`, `get`, `list` (sort, cursor; search if a
search concept proves useful), `update`, `delete`, `archive`/`unarchive`,
`bulk_update`. Keep `start`/`approve` as the scoped-bulk-promote reference
pattern — don't touch or generalize those, they're already good. Add
budget-range filtering.

## Scope

- **`list`**: apply PRIM-001 (dead `Limit`/`Offset` in `SprintFilter`
  today) and PRIM-002 (sort, docstring/order mismatch already fixed by
  FIX-004 — confirm consistency). Evaluate whether a free-text search
  concept is actually useful for Sprint (ADR says "if a search concept
  proves useful" — this is a judgment call, not a hard requirement; if
  skipped, note why in the implementation).
- **Budget-range filter**: audit notes "no filter by cost-budget range or
  'over budget'" — add this to `list`.
- **Archive/unarchive**: apply PRIM-004's pattern.
- **`bulk_update`**: apply PRIM-003's pattern.
- **Registration**: apply DEC-002's decision.
- **Do not touch** `Start`/`ApproveAll` — audit explicitly calls these out
  as "a genuinely good bulk-convenience pattern... Service layer owns all
  logic cleanly; adapter is thin throughout." Leave as-is.

## Acceptance criteria

- [x] `torque_sprint_list` supports pagination (PRIM-001) and sort
      (PRIM-002).
- [x] Budget-range filter (e.g. `cost_budget_min`/`max` or equivalent)
      exists on `list`.
- [x] `archive`/`unarchive` tools exist per PRIM-004.
- [x] `bulk_update` exists per PRIM-003.
- [x] Tool registration matches DEC-002.
- [x] `torque_sprint_start`/`torque_sprint_approve_all` (or equivalent
      names) are unchanged by this task.

## Out of scope

- Any change to Sprint's `start`/`approve` convenience-bulk methods.

## Execution notes

**Worktree/branch note (not part of the scoped work, but load-bearing):**
this task's worktree was branched from `main` before PRIM-001–004,
FK-002/003, and FIX-001–005 were merged, so none of the "already built"
primitives this task's brief said to consume actually existed on disk
(`sqlstore/sprints.go` had no `archived_at`, `SprintService.List` was still
`(status, projectID string, includeArchived bool)`, no `pagination` package,
no `bulk.go`). Confirmed via `git log --all` that the real merges exist on
local `main` (`2c2df37` PRIM-004, `0298ff6` PRIM-003, `ff6fd30`
PRIM-001+002) and that the worktree's base commit is a strict ancestor of
`main` with no divergent commits of its own, so `git merge --ff-only main`
was safe and brought them in cleanly. Everything below was built against
that up-to-date base.

### `list`: pagination, sort, cursor (PRIM-001/PRIM-002)

- `SprintService.List` signature changed from `(status, projectID string,
  includeArchived bool)` to `(filter sqlstore.SprintFilter)
  ([]sqlstore.SprintRecord, error)` — mirrors `TaskService.List(filter
  sqlstore.TaskFilter)` exactly, so the method doesn't grow a new positional
  parameter every time a filter dimension is added. Updated all 5 call
  sites: `mcpadapter/sprint_tools.go`, `httpserver/sprints.go`,
  `service/sprint_test.go` (×3).
- `sqlstore.SprintFilter` gained `SortBy`/`SortDir`/`AfterSortValue`/
  `AfterID` (cursor pagination) plus `CostBudgetMin`/`CostBudgetMax`/
  `OverBudget` (budget filter, see below). `ListSprints` grew
  `sprintSortColumn`/`sprintCursorArg` helpers mirroring `taskSortColumn`/
  `taskCursorArg` in `tasks.go` — allow-list `name|status|updated_at|
  created_at` (the exact list `pagination.ValidateSortBy`'s own doc comment
  names for Sprint/Epic/Project), tuple-comparison cursor predicate
  `(sortCol, id) > (arg, AfterID)`, tiebreak always on `id ASC` regardless
  of `sort_dir` (DEC-001). Default order when no `sort_by` is given stays
  `updated_at DESC` — the order FIX-004 already confirmed matched the
  docstring, so no fix was needed there, just confirmation (verified by
  reading `ListSprints`'s pre-existing `ORDER BY updated_at DESC` before
  editing).
  - No `updatedAtNow()`-style write-format workaround was needed here: every
    Sprint write path (`UpdateSprint`, `TransitionSprint`, `ArchiveSprint`,
    `UnarchiveSprint`) sets `updated_at = CURRENT_TIMESTAMP` SQL-side and
    `created_at`/`updated_at` both default to `CURRENT_TIMESTAMP` at the
    schema level — unlike Task, there's no Go-side `time.Time` write to this
    column that could drift from SQLite's own text format.
- `torque_sprint_list` gained `sort_by`/`sort_dir`/`cursor`/`limit`
  (default 100, max 500 — `defaultGenericListLimit`/`maxGenericListLimit`,
  since Sprint is an opt-in "generic" entity, not Task's higher-traffic
  surface) using the exact `taskListCursorEnvelope`/`cappedCursorJSONResult`
  pattern (`sprintListCursorEnvelope`, `sprintSortValue` mirror
  `taskListCursorEnvelope`/`taskSortValue`). Fetches `limit+1` to compute
  `has_more` without a `COUNT(*)`, per DEC-001's cheaper default.

### Search: not added, by design

Evaluated per the ADR's hedge ("search if a search concept proves useful")
and decided **not** to add a `search` param/tool for Sprint. Reasoning:
Sprint is a low-cardinality entity — a project realistically has a handful
of active/inactive sprints at a time, not the hundreds-to-thousands of rows
Task's `torque_task_search` exists to handle. `status`/`project_id`/the new
budget filters plus a default 100-row `list` page already make any sprint
easy to find without free-text matching on `name`/`goal`. The audit's own
Sprint findings section doesn't flag missing search as a gap (only dead
pagination and the budget filter), unlike Task where search has a dedicated
audit thread. If sprint volume per project ever grows past what a single
`list` page comfortably shows, `Search` on `TaskFilter`
(id/title/description substring) is the template to follow.

### Budget-range filter (audit: "no filter by cost-budget range or 'over
budget'")

- `SprintFilter` gained `CostBudgetMin`/`CostBudgetMax *float64` (compare
  directly against `cost_budget`; a `NULL` `cost_budget` row never matches
  either bound — SQL `NULL` comparison — so a sprint with no budget set
  correctly never shows up in a budget-range query) and `OverBudget bool`
  (sprints with `cost_budget IS NOT NULL AND cost_budget <` a correlated
  subquery summing `runs.cost` for the sprint's tasks — the same join
  `SprintCostUsed` already uses, just inlined as a `WHERE`-clause
  correlated subquery instead of a second round-trip per row).
- `torque_sprint_list` exposes `cost_budget_min`/`cost_budget_max`/
  `over_budget`.

### Archive/unarchive (PRIM-004)

Store (`ArchiveSprint`/`UnarchiveSprint`) and service
(`SprintService.Archive`/`Unarchive`) methods already existed post-merge —
this task only wired the MCP surface: `torque_sprint_archive`/
`torque_sprint_unarchive`, following `torque_collection_archive`/
`_unarchive`'s response shape (`{id, archived: true, message}` /
`{id, unarchived: true, message}`) as the reference pattern, per PRIM-004's
own execution notes pointing at Collection. `torque_sprint_list` also
gained `include_archived` (default false, excludes `archived_at IS NOT
NULL` rows) — this was previously hardcoded `false` with a comment noting
Phase 4 owned wiring it; that comment is now resolved.

One description-length fix needed: `torque_sprint_unarchive`'s first draft
was 3 lines and failed
`TestDescriptionInventory_AssertsMinimumsAndEmitsArtifact`'s >=4-line Phase C
gate (this repo's `descriptions_test.go` enables `sprints` but not
`collections`, so the pre-existing 3-line `torque_collection_unarchive`
description never tripped this gate — it's not a bug I introduced, just one
this task's new tool newly exposed to the check). Fixed by adding a second
sentence.

### `bulk_update` (PRIM-003)

- `SprintService.BulkUpdate(ids []string, update sqlstore.SprintUpdate,
  status string) ([]string, []BulkItemError)` — built on `RunBulk`
  (`bulk.go`), mirroring `TaskService.BulkUpdate`'s shape in
  `task_bulk.go`. Per-id: if `status` is non-empty, `Transition` runs first
  (through that sprint's own current-status FSM — two sprints in different
  states can legitimately diverge), then the field update (if any field was
  set) applies via `Update` — this matches `handleSprintUpdate`'s existing
  single-item combined semantics exactly, just looped.
- A single `feature.Require("sprints")` check up front (rather than relying
  on the per-call checks already inside `Transition`/`Update`) means a
  disabled feature fails every id uniformly with the same
  `FeatureDisabledError`, following `TaskService.BulkTag`'s precedent in
  `task_bulk.go` for a shared precondition that isn't really "per item."
- `torque_sprint_bulk_update` reuses `reqIDs`/`bulkResult` from
  `task_bulk_tools.go` (PRIM-003's shared adapter helpers) and a new
  `buildSprintUpdate(req) (sqlstore.SprintUpdate, bool)` helper extracted
  from — and now also used by — the existing single-item
  `handleSprintUpdate`, so the two handlers' field semantics can't drift
  apart (same rationale as Task's `buildTaskUpdateInput` being shared
  between single/bulk). Field-detection semantics were preserved exactly as
  they were pre-refactor (value-based for
  name/goal/approval_mode/cost_budget, presence-based via `reqHasArg` for
  `project_id`) — **not** upgraded to Task's FIX-001 presence-based
  semantics, since fixing that mismatch for Sprint is out of this task's
  scope (FIX-001 was Task-specific) and changing single-item
  `torque_sprint_update`'s existing behavior wasn't asked for here.
  **Update (SWEEP-001):** this exact mismatch was flagged by the Phase 5
  consistency sweep and fixed there — `buildSprintUpdate` is now
  presence-based (`reqHasArg`) for all five fields, matching Task's
  FIX-001 semantics; see `tasks/phase-5-consistency/SWEEP-001-envelope-and-error-taxonomy-audit.md`.

### Registration (DEC-002)

New tools (`torque_sprint_archive`, `torque_sprint_unarchive`,
`torque_sprint_bulk_update`) registered inside the existing
`registerSprintTools()` in `internal/mcpadapter/sprint_tools.go`, called
from `registerOptInTools()` exactly as before — no change to the
feature-flag gating structure.

### `torque_sprint_start`/`torque_sprint_approve` — confirmed untouched

`handleSprintStart`, `handleSprintApprove`, and their doc comments are
byte-for-byte unchanged from before this task (diffed the final file
against the pre-task version to confirm). `SprintService.Start`/
`ApproveAll`/`ApproveTask` were not touched either.

### Files touched

- `internal/persistence/sqlstore/sprints.go` (filter fields, sort/cursor
  helpers, `ListSprints` rewrite for budget filter + cursor)
- `internal/persistence/sqlstore/sprints_test.go` (budget-range,
  over-budget, cursor-tiebreak, invalid-cursor tests)
- `internal/service/sprint.go` (`List` signature change, `BulkUpdate`)
- `internal/service/sprint_test.go` (call-site arity fixes;
  `BulkUpdate`/budget-filter tests)
- `internal/mcpadapter/sprint_tools.go` (list/archive/unarchive/bulk_update
  tools + handlers, `buildSprintUpdate` extraction)
- `internal/httpserver/sprints.go` (call-site arity fix only —
  `listSprints` now builds a `sqlstore.SprintFilter`; no new HTTP query
  params wired, matching PRIM-004's precedent of touching HTTP only enough
  to keep it compiling)
- `tasks/phase-4-entities/ENT-SPRINT.md` (this file)

### Verification

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./... -count=1` — all packages pass, including the new
  store-layer (`sprints_test.go`) and service-layer (`sprint_test.go`)
  tests for budget filtering, over-budget, cursor pagination/tiebreak,
  invalid-cursor, and bulk update (field-only, combined with status
  transition, partial-failure, feature-gated).

### Issues / blockers

None outstanding. The one real snag (worktree branched before the
depended-on primitives were merged into `main`) was resolved by
fast-forwarding the worktree branch onto `main` before starting — see the
note at the top of this section.
