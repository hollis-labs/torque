# FIX-003 — Fix Epic status vocabulary bug

**Phase:** 1 — Locked bug fixes
**Status:** todo
**Depends on:** none
**Blocks:** ENT-EPIC
**Source:** ADR-0004 §5 (Epic); audit "Per-Entity Findings → Epic [high]",
"Proposed Direction G.1"

## Summary

Confirmed correctness bug, independent of the broader ergonomics work:
`torque_epic_update`'s own docstring and example
(`{"id":"EP-4","status":"closed"}`, "open<->closed transitions",
`epic_tools.go:31-38`) will **always fail validation** —
`EpicService.Update` only accepts `validEpicStatuses = {active, inactive}`
(`service/epic.go:33-36`). The DB schema's own default is
`status TEXT NOT NULL DEFAULT 'open'`
(`migrations/001_initial.sql:127`), but `CreateEpic` overrides that to
`"active"` in Go (`epics.go:41-43`) — so `"open"` is dead too.

## Scope

Reconcile the enum one way or the other:
- **Option A**: change `validEpicStatuses` to `{open, closed}` (or whatever
  set matches the tool's documented example), update `CreateEpic`'s default
  accordingly, and confirm the DB default stays consistent.
- **Option B**: keep `active`/`inactive` as the real enum and fix the tool
  docstring/example on `torque_epic_update` to stop advertising
  `open`/`closed`.

Pick whichever is closer to how Epic status is actually used/displayed
elsewhere (GUI, HTTP) — check for existing UI copy or HTTP contract that
already commits to one vocabulary before choosing, to avoid introducing a
second mismatch.

## Acceptance criteria

- [ ] `torque_epic_update`'s docstring example is runnable as documented
      (whichever vocabulary is chosen, the example uses real, accepted
      values).
- [ ] `EpicService.Update`'s valid-status set, `CreateEpic`'s default, and
      the DB column default (`001_initial.sql`) all agree.
- [ ] Existing epics with the old vocabulary value in the DB still load and
      display correctly (no migration needed if only the accepted-input set
      changes, but double check nothing reads the raw DB value expecting
      the old vocabulary elsewhere).

## Out of scope

- Epic `priority` settability (ENT-EPIC, not this bug fix).
- Epic archive/unarchive (PRIM-004 / ENT-EPIC).
