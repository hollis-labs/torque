# FK-003 — Normalize `depends_on` into a real join table

**Phase:** 3 — Real FK migration
**Status:** todo
**Depends on:** none (independent of FK-001/FK-002 — different columns,
different migration; can run in parallel)
**Blocks:** none directly (ENT-TASK proceeds without touching `depends_on`
— its create-field-expansion work doesn't require this to land first)
**Source:** ADR-0004 §4 (Task field reference), §6 (Open questions);
[DEC-003](../phase-0-decisions/DEC-003-depends-on-normalization.md)
(decision + investigation this task is split out of)

## Summary

`tasks.depends_on` is a JSON array of task IDs inside a single `TEXT`
column. DEC-003 investigated this end-to-end and found the staleness risk
is worse than ADR-0004 originally framed it: `internal/runtime/scheduler/
picker.go:257-281` uses `depends_on` to gate whether a task is eligible to
run, and since nothing ever prunes a stale ID when the referenced task is
deleted, **a task whose dependency is later deleted becomes permanently,
silently unschedulable** — a live scheduler deadlock bug, not just dangling
metadata. DEC-003 decided (accepted by the project owner) to fix this by
normalizing `depends_on` into a real join table with `ON DELETE CASCADE`,
which prunes the edge automatically when the dependency is deleted.

## Precedent

`internal/persistence/sqlstore/migrations/005_tags.sql` already did this
exact promotion for `tasks.tags`: created `tags` + `task_tags` (composite
PK, `FOREIGN KEY ... ON DELETE CASCADE` both directions), then
`ALTER TABLE tasks DROP COLUMN tags`. Follow that migration as the template
— this is lower-risk than FK-002's in-place column promotion (no full
`tasks_new` rebuild needed).

## Scope

- **Migration:** new `task_dependencies(task_id, depends_on_task_id)` join
  table, composite PK, `FOREIGN KEY (depends_on_task_id) REFERENCES
  tasks(id) ON DELETE CASCADE` (prunes the edge when the dependency is
  deleted — fixes the `picker.go` deadlock) and `FOREIGN KEY (task_id)
  REFERENCES tasks(id) ON DELETE CASCADE` (prevents orphaned join rows when
  the dependent task itself is deleted — a second, smaller dangling-
  reference direction not called out in the ADR). `ALTER TABLE tasks DROP
  COLUMN depends_on` once the new path is live, following `005_tags.sql`.
- **`internal/service/task.go`:** update Create/Update to read/write the
  join table instead of marshaling/unmarshaling a JSON column.
- **`internal/service/task_validation.go`:** update the existence check
  (currently N+1 `GetTask` lookups against the JSON array) to query the
  join table — same conceptual check, different source.
- **`internal/persistence/sqlstore/tasks.go`:** update scan/insert paths
  that currently read/write the JSON column.
- **`internal/mcpadapter/task_tools.go`:** update the `depends_on`
  JSON-string encode/decode at the MCP boundary (~lines 325-327, 510,
  541-542). The tool-facing contract (a JSON array of task IDs in the
  request/response) can likely stay unchanged even though the storage
  underneath changes — that's a plus for callers, not a breaking change.
- **`internal/runtime/scheduler/picker.go`:** update the dependency-gating
  read path (~lines 257-281) to query the join table instead of
  unmarshaling JSON. This is the fix for the live deadlock bug.
- **Cycle detection (decide, don't just carry forward the gap):**
  `depends_on` has no cycle detection today, unlike `parent_id`
  (`TaskService.validateParentID`, `internal/service/task.go:496-522`,
  which walks ancestors to reject cycles). Decide whether the join table
  should get the same cycle guard, or whether that's explicitly deferred
  again — don't leave it ambiguous.

## Acceptance criteria

- [ ] `task_dependencies` join table exists with both `ON DELETE CASCADE`
      directions; `tasks.depends_on` column is dropped.
- [ ] Deleting a task that other tasks depend on automatically removes the
      dependency edge (verified with a real delete, not just reading the
      schema) — confirm this actually unblocks the previously-deadlocked
      dependent task on the scheduler's next tick.
- [ ] Deleting a task that itself has dependencies leaves no orphaned
      `task_dependencies` rows.
- [ ] `torque_task_create`/`update`'s `depends_on` param behavior is
      unchanged from the caller's perspective (same JSON-array-of-IDs
      contract), even though storage changed underneath.
- [ ] Existence validation on write still rejects a `depends_on` entry
      pointing at a nonexistent task.
- [ ] Cycle detection decision made explicitly (built, or deferred with a
      documented reason) — not silently left as before.
- [ ] Full test suite passes, including scheduler tests that exercise
      dependency gating (`internal/runtime/scheduler`).

## Out of scope

- `sprint_id`/`project_id`/`epic_id` FK promotion — that's FK-001/FK-002,
  a separate track.
- Any change to `parent_id`'s existing cycle-detection pattern beyond
  deciding whether `depends_on` should mirror it.

## Outcome

_(fill in when resolved)_
