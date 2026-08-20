# ENT-SUBTODO — Subtodo: bulk_add

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** FIX-002 (id auto-gen must land first)
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Subtodo); audit "Per-Entity Findings → Subtodo"

## Summary

Subtodo's target tool inventory per ADR-0004: `add` (auto-gen id, FIX-002),
`list`, `update`, `done`, `delete`, `bulk_add`. Everything except
`bulk_add` already exists. This task is narrowly scoped to that one gap.

## Scope

- `bulk_add` — seed a whole checklist onto a single task in one call,
  reusing FIX-002's auto-gen-id logic per item (and still honoring
  caller-supplied ids per item where given, same rule as single `add`).
- Response shape: match PRIM-003's `{succeeded: [...], failed: [{id,
  error}]}` pattern if it fits cleanly (each item here is a new subtodo,
  not an update to an existing id, so "succeeded" would be the generated/
  confirmed ids rather than input ids — adapt sensibly, note the deviation
  if any).

## Acceptance criteria

- [x] `torque_task_subtodo_bulk_add` (or equivalent name matching the
      `torque_<entity>_<verb>` convention) accepts an array of subtodo
      specs and creates them all against one task in a single call.
- [x] Mixed caller-supplied-id and omitted-id items in the same batch both
      work correctly.
- [x] Partial failure (e.g. one duplicate id in the batch) doesn't abort
      the whole batch — matches the bulk partial-success principle used
      elsewhere.

## Out of scope

- Cross-task subtodo query surface — ADR explicitly keeps Subtodo
  task-scoped only (§1 item 4, "not revisited").
- Author/timestamp fields on `Subtodo` (audit notes this as low-priority,
  thinner audit trail than Comment/Checkpoint — not in ADR-0004's target
  inventory, don't add speculatively).

## Execution notes

**Worktree was stale.** This worktree's branch tip (`c57775d`, "Docs
sync") predated FIX-002, PRIM-003, and the `tasks/` directory itself — all
landed on local `main` (33 commits ahead) after this worktree branched.
Confirmed `HEAD` was a strict ancestor of `main` (`git merge-base
--is-ancestor HEAD main`) with no divergent commits, so fast-forwarded via
`git merge main --ff-only` (non-destructive) before starting; `tasks/`
came in through that merge.

**Tool:** `torque_task_subtodo_bulk_add` — `task_id` (required) +
`items` (required, JSON array of `{id?, text, required?}` specs). Per-item
id resolution reuses FIX-002's `newSubtodoID`/`subtodoIDTaken` exactly:
caller-supplied id validated for uniqueness (against the task's existing
checklist *and* earlier items already accepted in the same batch),
omitted id auto-generated with the `sub_` prefix. Registered/handled in
`internal/mcpadapter/subtodo_tools.go`
(`registerSubtodoTools`/`handleSubtodoBulkAdd`); service logic in
`internal/service/task.go` (`TaskService.BulkAddSubtodo`).

**Response shape — deviation from PRIM-003's literal `RunBulk` reuse, as
flagged in this task's own Scope section:** `RunBulk` (`internal/
service/bulk.go`) closes over an `id` the caller already has —
`bulk_update`/`bulk_delete`/`bulk_tag` all key off an existing `ids[]`.
`bulk_add`'s items are *new*; most arrive with no id at all, so there's no
pre-existing id for `RunBulk`'s `op` to take. `BulkAddSubtodo` loops
directly over the item specs instead of calling `RunBulk`, but still
returns the same top-level shape at the adapter layer by feeding its
`(succeeded []string, failed []service.BulkItemError)` result straight
into the existing `bulkResult` helper
(`internal/mcpadapter/task_bulk_tools.go`) unchanged — no new response
type. The one deliberate difference in *meaning*: `succeeded` holds each
item's *resolved* id (caller-supplied or generated), not an echo of an
input id, since bulk_add assigns ids rather than looking them up. A
failed item reports its caller-supplied id when given, else a positional
placeholder (`item[N]`) since no id was ever assigned. Doc'd inline on
`BulkAddSubtodo` and in the tool's MCP description.

**Efficiency note:** `BulkAddSubtodo` fetches the checklist once, applies
every item against one in-memory copy (validating id/text and detecting
both pre-existing and intra-batch duplicate ids), and persists with a
single `SetSubtodos` call — not one read+write per item. If that final
write itself fails (e.g. task not found), every provisionally-accepted
item reverts to `failed` rather than reporting ids that were never
actually persisted.

**Tests added:**
- `internal/service/subtodo_service_test.go` —
  `TestTaskService_BulkAddSubtodo_MixedCallerAndOmittedIDs` (mixed
  caller-supplied/omitted ids in one batch),
  `TestTaskService_BulkAddSubtodo_PartialFailureDoesNotAbortBatch`
  (intra-batch duplicate, pre-existing-id duplicate, and missing-text all
  fail independently while the rest of the batch lands),
  `TestTaskService_BulkAddSubtodo_UnknownTask` (whole-batch failure path
  when `task_id` doesn't exist).
- `internal/mcpadapter/subtodo_tools_test.go` —
  `TestSubtodoBulkAdd_MixedCallerAndOmittedIDs`,
  `TestSubtodoBulkAdd_PartialFailureDoesNotAbortBatch`,
  `TestSubtodoBulkAdd_ArgErrors` (empty/malformed `items[]` is a
  call-level `arg_invalid`, matching `reqIDs`'s existing rule for empty
  `ids[]`).

**Results:** `go build ./...` and `go test ./...` both pass (full suite,
all packages), `go vet ./...` clean, `gofmt -l` clean on all changed
files. No blockers.
