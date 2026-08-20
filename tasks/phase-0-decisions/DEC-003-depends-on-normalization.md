# DEC-003 — `depends_on` normalization: join table vs documented JSON blob

**Phase:** 0 — Decisions
**Status:** todo
**Depends on:** none
**Blocks:** ENT-TASK
**Source:** ADR-0004 §4 (Task field reference), §6 (Open questions)

## Summary

`depends_on` is a JSON array of task IDs inside a single TEXT column.
Existence is checked at write time, but nothing stops a referenced task from
being deleted later, leaving a dangling ID inside the array. Promoting it to
a real join table would enable an actual FK but is, per the ADR, "a bigger
structural change" than the sprint_id/project_id/epic_id promotion in
FK-001/FK-002 (those are single-value columns on an existing table; this is
a one-to-many normalization).

## Decision to make

1. **Normalize to a join table** (e.g. `task_dependencies(task_id,
   depends_on_task_id)`), matching the `parent_id`/real-FK pattern used
   elsewhere. Real referential integrity, but a new migration + new
   read/write path replacing the JSON-array accessor.
2. **Keep as JSON blob, document the staleness risk.** No schema change.
   ENT-TASK's create-field-expansion work leaves `depends_on` as-is; add a
   docstring note on `torque_task_create`/`update` that a referenced task
   being deleted can leave a stale ID behind, and that callers should treat
   `depends_on` as best-effort, not enforced.

## Acceptance criteria

- [ ] Decision recorded with rationale.
- [ ] If option 1: scope noted separately from ENT-TASK (this is materially
      bigger than the rest of ENT-TASK's scope — consider whether it
      deserves its own task file split out from ENT-TASK rather than folded
      in, and note that decision here).
- [ ] If option 2: the exact docstring language to add is drafted here so
      ENT-TASK can lift it directly.
- [ ] ENT-TASK unblocked.

## Out of scope

Implementation — that lands in ENT-TASK (option 2) or a newly-split task
(option 1, if chosen).

## Outcome

_(fill in when resolved)_
