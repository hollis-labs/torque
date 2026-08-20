# PRIM-002 — Sort primitive (`sort_by`/`sort_dir`)

**Phase:** 2 — Shared primitives
**Status:** todo
**Depends on:** none (independent of DEC-001; coordinate with PRIM-001 on
cursor tiebreak semantics if cursor pagination was chosen)
**Blocks:** ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT,
ENT-ISSUE, ENT-PLAN
**Source:** ADR-0004 §3 (Sorting); audit "Cross-Cutting Findings §2
(Sorting)", "Proposed Direction B"

## Summary

Zero `OrderBy`/`SortBy` fields exist anywhere in `internal/persistence/
sqlstore` today. Build the shared `sort_by` (allow-listed per entity) /
`sort_dir` (`asc`/`desc`) mechanism once, with Task as the reference
implementation.

## Scope

- Add `OrderBy sort_by` + `sort_dir` support to the store-layer filter
  structs, starting with Task's (`TaskFilter`, `tasks.go`).
- Build a small shared validation helper: given an entity's allow-list
  (e.g. Task: `priority, status, updated_at, created_at`; Epic/Sprint/
  Project: `name, status, updated_at, created_at`), reject unknown
  `sort_by` values with a clean `arg_invalid` error rather than silently
  ignoring or erroring at the SQL layer.
- If PRIM-001 chose cursor pagination: confirm the cursor's tiebreak-on-`id`
  design still holds for every sort column, not just the default — a
  cursor keyed to `updated_at` needs `(updated_at, id)` as the real sort
  key, not just `updated_at`.
- Apply to **Task's `list`** as the reference implementation, alongside
  PRIM-001.
- Document each entity's actual default order accurately regardless of what
  else lands — this closes the same ground as FIX-004 but as a byproduct;
  if PRIM-002 for a given entity lands after FIX-004 already fixed that
  entity's docstring, just confirm consistency rather than redoing it.

## Acceptance criteria

- [ ] `torque_task_list` accepts `sort_by` + `sort_dir`, validated against
      an explicit allow-list, with a clean error on an invalid value.
- [ ] Default order (no `sort_by` given) matches what the docstring says
      (coordinate with FIX-004 so these don't contradict).
- [ ] The allow-list + validation helper is reusable, not copy-pasted
      per-entity in Phase 4.
- [ ] If cursor pagination is in play, paging through a `sort_by=priority`
      list with duplicate priority values doesn't produce duplicate/missing
      rows across pages.

## Out of scope

- Rolling out to the other 6 entities — Phase 4.
- Filter/facet closure beyond sorting (separate ADR concern, folded into
  each entity's Phase 4 task where relevant).
