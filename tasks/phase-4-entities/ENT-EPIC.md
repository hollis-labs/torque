# ENT-EPIC — Epic: merge list+search, settable priority, archive, bulk_update

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** FIX-003, FIX-004, PRIM-001, PRIM-002, PRIM-003, PRIM-004,
DEC-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Epic); audit "Per-Entity Findings → Epic"

## Summary

Epic target inventory: `create`, `get`, `list` (merged search, sort,
cursor), `update`, `delete`, `archive`/`unarchive`, `bulk_update`.
FIX-003 (status vocabulary bug) must land first since it touches the same
`update` path this task extends.

## Scope

- **`list`**: apply PRIM-001 (pagination — dead `Limit`/`Offset` in
  `EpicFilter` today) and PRIM-002 (sort). No epic search exists today (no
  analog to Task/Issue's `_search`) — confirm whether a merged
  list+search is actually needed for Epic or whether `list`'s filter/search
  param alone covers it; ADR §5 says "merged search, sort, cursor" so build
  free-text search into `list` directly rather than a separate tool.
- **`priority` settable**: exists on `EpicRecord`, `EpicCreateInput`, and
  `EpicUpdateInput`, even surfaced in `briefEpic` list responses — but
  neither `torque_epic_create` nor `torque_epic_update` expose a `priority`
  param today (`epic_tools.go:12-40`). Wire it on both.
- **Archive/unarchive**: apply PRIM-004's pattern.
- **`bulk_update`**: apply PRIM-003's pattern.
- **Registration**: apply DEC-002's decision.

## Acceptance criteria

- [x] `torque_epic_list` supports free-text search, pagination (PRIM-001),
      and sort (PRIM-002) in one tool — no separate `_search` tool created.
- [x] `torque_epic_create` and `torque_epic_update` both accept `priority`;
      round-trips correctly (set on create, changeable on update, visible
      in list/get responses — the last part already works).
- [x] `archive`/`unarchive` tools exist per PRIM-004.
- [x] `bulk_update` exists per PRIM-003.
- [x] Tool registration matches DEC-002.
- [x] FIX-003's status-vocabulary fix is confirmed still consistent after
      this task's `update` changes (don't reintroduce the mismatch).

## Out of scope

- None beyond what's listed — Epic's target inventory is fully covered by
  this task plus FIX-003/FIX-004.

## Execution notes

**Corrective-redo context:** a prior attempt at this task ran in a worktree
branched from a stale point in `main` history — before PRIM-001..004 and
FIX-003 were merged — concluded (incorrectly) that those primitives didn't
exist, and rebuilt duplicate copies of shared infrastructure. That attempt
was not merged. This run started from a worktree that turned out to *also*
be stale (checked out 33 commits behind the true `main` tip, missing
`tasks/`, `internal/service/pagination`, `internal/service/bulk.go`, and
PRIM-004's archive columns entirely) — verified directly via
`git merge-base --is-ancestor` before writing any code, rather than trusting
the dispatch prompt's "already merged" claim. Fixed by fast-forwarding the
worktree's branch onto the real `main` tip (`git merge --ff-only`, a clean
fast-forward since the worktree had zero local commits of its own) before
starting implementation. All primitives referenced below were then
confirmed to genuinely pre-exist in `main` — none were rebuilt.

### What was built

- **`list`** (`internal/mcpadapter/epic_tools.go` `handleEpicList` +
  `internal/service/epic.go` `EpicService.ListPaginated` +
  `internal/persistence/sqlstore/epics.go` `ListEpics`/`EpicFilter`):
  added `search` (case-insensitive substring on id/name/description),
  `sort_by` (`name|status|updated_at|created_at`, matching the audit's
  documented Epic/Sprint/Project allow-list — deliberately excludes
  `priority`, which is not on that list), `sort_dir`, `cursor`, `limit`,
  and `include_archived` params. Default order unchanged (`updated_at
  DESC`). Implementation mirrors Task's PRIM-001/PRIM-002 reference
  (`handleTaskList`/`taskListCursorEnvelope`/`taskSortValue` in
  `task_tools.go`) closely: `epicSortColumn`/`epicCursorArg` helpers in
  `epics.go` mirror `taskSortColumn`/`taskCursorArg`; `epicSortValue`/
  `epicListCursorEnvelope` in `epic_tools.go` mirror `taskSortValue`/
  `taskListCursorEnvelope`; the handler over-fetches `limit+1` and calls
  the existing `cappedCursorJSONResult` from `response.go` unchanged.
  `EpicService.List` (the old 3-arg signature) is left untouched for its
  one existing caller (HTTP's `listEpics`) — `ListPaginated` is new and
  additive, both funnel into the same (now-extended) `sqlstore.EpicFilter`/
  `ListEpics`.
- **`priority`**: `torque_epic_create`/`torque_epic_update` both gained a
  `priority` string param, parsed via a new presence-based
  `reqEpicPriorityPtr` helper (so an explicit `priority="0"` isn't dropped
  the way a value-based `!= 0` check would — the same class of bug FIX-001
  fixed on Task). `name`/`description`/`status` on `torque_epic_update`
  were left on their existing value-based detection (empty string = not
  provided) — fixing that too was not in this task's acceptance criteria
  and would have been unrelated scope creep.
- **`archive`/`unarchive`**: new `torque_epic_archive`/
  `torque_epic_unarchive` MCP tools wired onto `EpicService.Archive`/
  `Unarchive`, which already existed (PRIM-004, unchanged by this task).
  Response shape mirrors `torque_collection_archive`/`unarchive`
  (`collection_tools.go`), PRIM-004's own reference pattern.
  `include_archived` was added to `torque_epic_list`'s schema in the same
  pass (was previously hardcoded to `false` with a comment flagging it as
  "Phase 4's job").
- **`bulk_update`**: new `torque_epic_bulk_update` MCP tool +
  `EpicService.BulkUpdate` (new file `internal/service/epic_bulk.go`,
  mirroring `task_bulk.go`'s layout), built on the existing
  `service.RunBulk` (`bulk.go`) and shaped through the existing
  `bulkResult`/`reqIDs` helpers in `task_bulk_tools.go` (same package,
  reused directly — no new bulk-response type). A shared
  `buildEpicUpdateInput` helper factors the update-field parsing out of
  `handleEpicUpdate` so single- and bulk-update can't drift apart on
  semantics, mirroring `buildTaskUpdateInput`.
- **Registration**: all six new/changed tools were added inside the
  existing `registerEpicTools()`, still called conditionally from
  `registerOptInTools()` in `adapter.go` exactly as before — no change to
  `adapter.go` itself, matching DEC-002 exactly (no new gating mechanism).
- **FIX-003 regression check**: `validEpicStatuses` (`service/epic.go`)
  and every status docstring touched by this task still say
  `active|inactive`; grepped for `open`/`closed` in the changed files —
  none reintroduced.

### Confirmed NOT rebuilt (reused as-is from `main`)

`internal/service/pagination` (`Cursor`/`Encode`/`Decode`/`Validate`,
`ValidateSortBy`/`ValidateSortDir`), `internal/service/bulk.go`
(`BulkItemError`/`RunBulk`), `internal/mcpadapter/response.go`'s
`cappedCursorJSONResult`, `EpicService.Archive`/`Unarchive` and
`Store.ArchiveEpic`/`UnarchiveEpic`, `EpicFilter.IncludeArchived`, and the
`030_archived_at.sql` migration — all pre-existing, all left untouched
except `EpicFilter` gaining the new `Search`/`SortBy`/`SortDir`/
`AfterSortValue`/`AfterID` fields alongside its existing ones.

### Files touched

- `internal/persistence/sqlstore/epics.go` (`EpicFilter` extended;
  `ListEpics` gained search/sort/cursor; new `epicSortColumn`/
  `epicCursorArg` helpers)
- `internal/persistence/sqlstore/epics_test.go` (+6 tests: search, default
  order, sort+cursor tiebreak, invalid cursor)
- `internal/service/epic.go` (+`EpicListInput`, +`ListPaginated`)
- `internal/service/epic_bulk.go` (new; +`BulkUpdate`)
- `internal/service/epic_test.go` (+5 tests: search, sort, include_archived,
  priority round-trip, bulk_update partial success)
- `internal/mcpadapter/epic_tools.go` (rewritten: priority params, list
  rewrite, archive/unarchive tools, bulk_update tool,
  `buildEpicUpdateInput` extraction)
- `internal/mcpadapter/epic_tools_test.go` (new; 11 full-stack tests
  covering priority, search, cursor pagination, invalid sort_by, cursor/
  sort mismatch rejection, archive/unarchive, bulk_update partial success
  and arg errors)
- `tasks/phase-4-entities/ENT-EPIC.md` (this file)

### Verification

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l` on all touched files — clean (no output).
- `go test ./...` — all packages pass, including the 22 new/changed Epic
  tests across sqlstore/service/mcpadapter. One pre-existing repo-wide test
  (`TestDescriptionInventory_AssertsMinimumsAndEmitsArtifact`, Phase C's
  ">= 4 lines + Example:" contract for every tool description) initially
  failed because `torque_epic_unarchive`'s description was 3 lines; fixed
  by adding a line, no functional change.

### Issues / blockers

None. `EpicService.List` (HTTP's call path) and its 3-arg signature were
left untouched; no other entity's files were touched.
