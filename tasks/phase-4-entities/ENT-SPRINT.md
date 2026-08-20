# ENT-SPRINT — Sprint: list sort/cursor, archive, bulk_update, budget filter

**Phase:** 4 — Per-entity rollout
**Status:** todo
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

- [ ] `torque_sprint_list` supports pagination (PRIM-001) and sort
      (PRIM-002).
- [ ] Budget-range filter (e.g. `cost_budget_min`/`max` or equivalent)
      exists on `list`.
- [ ] `archive`/`unarchive` tools exist per PRIM-004.
- [ ] `bulk_update` exists per PRIM-003.
- [ ] Tool registration matches DEC-002.
- [ ] `torque_sprint_start`/`torque_sprint_approve_all` (or equivalent
      names) are unchanged by this task.

## Out of scope

- Any change to Sprint's `start`/`approve` convenience-bulk methods.
