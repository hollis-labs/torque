# PRIM-003 — Generalized bulk-operation pattern

**Phase:** 2 — Shared primitives
**Status:** todo
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

- [ ] A reusable bulk-update/bulk-delete/bulk-tag pattern exists that
      Phase 4 entity tasks can apply without re-deriving the
      partial-success response shape each time.
- [ ] Applied to at least one entity end-to-end (suggest Task, since
      ENT-TASK needs `bulk_update`/`bulk_delete`/`bulk_tag` anyway) to prove
      the pattern before the others adopt it.
- [ ] Response shape for all three verbs matches
      `{succeeded: [...], failed: [{id, error}]}` exactly.
- [ ] Errors inside `failed[]` use the same error taxonomy
      (`arg_invalid`/`not_found`/`conflict`/`domain`/`permission`/
      `internal`) as single-item operations — no bulk-specific error codes.

## Out of scope

- Rolling out to Epic/Sprint/Issue — Phase 4.
- `bulk_transition` generalization beyond what Task already has (explicitly
  not part of this task per ADR §3).
