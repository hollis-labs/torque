# PRIM-003 — Generalized bulk-operation pattern

**Phase:** 2 — Shared primitives
**Status:** done
**Depends on:** none
**Blocks:** ENT-TASK, ENT-EPIC, ENT-SPRINT, ENT-ISSUE
**Source:** ADR-0004 §3 (Bulk operations); audit "Cross-Cutting Findings §4
(Bulk/batch operations)", "Proposed Direction D"

## Summary

`TaskService.BulkTransition` (`service/task.go:720`) is the one genuinely
good bulk pattern in the codebase: bulk logic lives in the service layer,
returns a per-item partial-success result
(`succeeded []string, errs []error`), and the adapter
(`task_tools.go:704`) just thin-wraps it. Generalize this shape into a
reusable pattern rather than hand-rolling bulk logic per entity per verb.

## Scope

Build a shared service-layer helper (or a documented pattern + shared
adapter-response shaping, if a literal generic helper doesn't fit Go's type
system cleanly — use judgment) covering:

- **`bulk_update`** — same field set as single `update`, applied across
  `ids[]`, true partial patch semantics per item (presence-in-payload, not
  value-based — same rule as single update).
- **`bulk_delete`** — where hard delete is kept at all (see PRIM-004 —
  entities that get `archive` may not need `bulk_delete` as urgently, but
  keep both available per the ADR's design principles).
- **`bulk_tag`** — add/remove tag slugs across many IDs in one call
  (reuses existing `TagService`, per ADR §4 "already done right
  structurally").
- All bulk ops return `{succeeded: [...], failed: [{id, error}]}` — partial
  success is not an error state. Match this exact shape everywhere so
  agents don't have to learn per-entity variants.
- `bulk_transition` is **not** part of this generic helper — ADR-0004 keeps
  it as its own verb only where a real FSM exists to protect (Task, and
  Issue since it shares Task's FSM). `TaskService.BulkTransition` already
  exists; Issue reuses it directly in ENT-ISSUE, no new primitive work
  needed there.

## Acceptance criteria

- [x] A reusable bulk-update/bulk-delete/bulk-tag pattern exists that
      Phase 4 entity tasks can apply without re-deriving the
      partial-success response shape each time.
- [x] Applied to at least one entity end-to-end (suggest Task, since
      ENT-TASK needs `bulk_update`/`bulk_delete`/`bulk_tag` anyway) to prove
      the pattern before the others adopt it.
- [x] Response shape for all three verbs matches
      `{succeeded: [...], failed: [{id, error}]}` exactly.
- [x] Errors inside `failed[]` use the same error taxonomy
      (`arg_invalid`/`not_found`/`conflict`/`domain`/`permission`/
      `internal`) as single-item operations — no bulk-specific error codes.

## Out of scope

- Rolling out to Epic/Sprint/Issue — Phase 4.
- `bulk_transition` generalization beyond what Task already has (explicitly
  not part of this task per ADR §3).

## Execution notes

**Reusable pattern** lives in two new service-layer files, entity-agnostic
so Phase 4 (ENT-EPIC, ENT-SPRINT, ENT-ISSUE) can adopt it directly:

- `internal/service/bulk.go` — `BulkItemError{ID, Err}` (keeps the
  *original typed* error per item — `*ValidationError`, `*NotFoundError`,
  sqlstore sentinels, etc. — not a stringified message) and
  `RunBulk(ids []string, op func(id string) error) (succeeded []string,
  failed []BulkItemError)`, the one partial-success loop behind every
  bulk_* verb. `TaskService.BulkTransition` (task.go:720) was **not**
  touched or refactored onto `RunBulk` — left exactly as-is per scope.
- `internal/mcpadapter/task_bulk_tools.go` — `bulkResult(succeeded
  []string, failed []service.BulkItemError)` shapes any `RunBulk` result
  into the canonical `{succeeded: [...], failed: [{id, error}]}` envelope.
  Each failure's error is run through the existing `mapServiceError` (the
  same taxonomy function `errFromService` uses for single-item ops), so
  `failed[].error.code` always matches what the identical single-item call
  would have returned. `reqIDs(req)` shares the `ids[]` JSON-array
  parse+validate (empty/malformed → call-level `arg_invalid`, not a
  per-item failure) across all three new tools.

**Applied to Task** (the file's own suggested proving ground):

- `TaskService.BulkUpdate`, `BulkDelete`, `BulkTag` —
  `internal/service/task_bulk.go`. `BulkUpdate`/`BulkDelete` are thin
  `RunBulk` wraps around the existing single-item `Update`/`Delete`.
  `BulkTag` adds/removes tag slugs per id (reusing `TagService.ResolveNames`
  for `add`, auto-create included, same as single-task tag assignment;
  `remove` is slugified directly via `strutil.Slugify` with no auto-create,
  so removing a slug that was never linked — or never existed — is a
  no-op, not a wasted tag row) against `store.ListTaskTags` /
  `store.SetTaskTags`, with an explicit `store.GetTask` existence check
  first so a missing id surfaces as a clean `not_found` instead of a raw
  FK-constraint error.
- `torque_task_bulk_update`, `torque_task_bulk_delete`, `torque_task_bulk_tag`
  — registered in `internal/mcpadapter/task_bulk_tools.go`
  (`registerTaskBulkTools`, wired from `adapter.go`'s `registerCoreTools`).
  `torque_task_bulk_update` takes the identical field set as
  `torque_task_update`.

**Presence-based `bulk_update` semantics (FIX-001 parity):** rather than
duplicate `handleTaskUpdate`'s ~200-line field-extraction logic, that logic
was extracted from `task_tools.go`'s `handleTaskUpdate` into a new
`buildTaskUpdateInput(req) (service.TaskUpdateInput, *mcp.CallToolResult)`
in the same file. Both `handleTaskUpdate` (single) and
`handleTaskBulkUpdate` (bulk) call it, so single- and bulk-update read
presence-in-payload identically by construction — they cannot drift apart.
This was the only edit inside `task_tools.go`; it touches just
`handleTaskUpdate`'s body (lines ~397-620), not `handleTaskList` or the
tool-registration block, to stay clear of the parallel cursor/sort work on
`torque_task_list` in the same file. `adapter.go` gets one added line
(`a.registerTaskBulkTools()`) next to the existing
`a.registerTaskTools()` call.

**Files touched:**
- `internal/service/bulk.go` (new)
- `internal/service/task_bulk.go` (new)
- `internal/service/bulk_test.go` (new)
- `internal/service/task_bulk_test.go` (new)
- `internal/mcpadapter/task_bulk_tools.go` (new)
- `internal/mcpadapter/task_bulk_tools_test.go` (new)
- `internal/mcpadapter/task_tools.go` (refactor: extracted
  `buildTaskUpdateInput` from `handleTaskUpdate`; no behavior change)
- `internal/mcpadapter/adapter.go` (+1 line: register the new tools)

**Verification:** `go build ./...` and `go test ./...` both green
(full repo, all packages). New coverage: `RunBulk` partial/all-
succeed/all-fail/empty-ids; `TaskService.BulkUpdate/BulkDelete/BulkTag`
partial success, nil-field-untouched partial-patch, tag add/remove
including the remove-never-existed no-op case, not-found-per-item; MCP
adapter tests for all three tools covering the exact `{succeeded, failed}`
shape, `failed[].error.code` = `not_found` for a bad id, presence-based
`bulk_update` (omitted key untouched vs explicit `""` clears, mirroring
FIX-001), and call-level `arg_invalid` for malformed/empty `ids` and
malformed JSON blob fields.

**Issues / blockers:** none. One judgment call worth flagging for Phase 4
adopters: `bulk_tag`'s `remove` list is *not* run through
`TagService.ResolveNames` (unlike `add`) specifically to avoid
auto-creating a tag just to immediately fail to find it on removal — future
entities' `bulk_tag` should follow the same asymmetry rather than resolving
both lists identically.
