# ENT-SUBTODO — Subtodo: bulk_add

**Phase:** 4 — Per-entity rollout
**Status:** todo
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

- [ ] `torque_task_subtodo_bulk_add` (or equivalent name matching the
      `torque_<entity>_<verb>` convention) accepts an array of subtodo
      specs and creates them all against one task in a single call.
- [ ] Mixed caller-supplied-id and omitted-id items in the same batch both
      work correctly.
- [ ] Partial failure (e.g. one duplicate id in the batch) doesn't abort
      the whole batch — matches the bulk partial-success principle used
      elsewhere.

## Out of scope

- Cross-task subtodo query surface — ADR explicitly keeps Subtodo
  task-scoped only (§1 item 4, "not revisited").
- Author/timestamp fields on `Subtodo` (audit notes this as low-priority,
  thinner audit trail than Comment/Checkpoint — not in ADR-0004's target
  inventory, don't add speculatively).
