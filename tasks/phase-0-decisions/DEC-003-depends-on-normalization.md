# DEC-003 — `depends_on` normalization: join table vs documented JSON blob

**Phase:** 0 — Decisions
**Status:** done
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

**DECIDED — accepted as proposed by the project owner.**

### Decision: Option 1 — normalize to a real join table

Split out of ENT-TASK into its own task file: [FK-003](../phase-3-fk-migration/FK-003-depends-on-join-table.md).

### Grounding in the current implementation

Before deciding, traced `depends_on` end-to-end rather than relying on the
ADR's summary alone:

- **Storage:** plain `TEXT` column on `tasks`, JSON-array-of-IDs
  (`internal/persistence/sqlstore/migrations/001_initial.sql:30`, carried
  through every `tasks_new` rebuild since — e.g.
  `migrations/025_kind_issue.sql:35`). Scanned/written as
  `sql.NullString` in `internal/persistence/sqlstore/tasks.go:65,166,`
  `228-286,545-547`.
- **Write-time validation:** `internal/service/task_validation.go:353-364`
  does an existence check per dependency ID (N+1 `GetTask` lookups,
  comment at line ~353 already flags this as "acceptable for typical"
  volumes) via `errors.Is(err, sqlstore.ErrTaskNotFound)`. This confirms
  the ADR's claim that existence is checked only at write time.
- **No delete-time cleanup:** `TaskService.Delete`
  (`internal/service/task.go:483-485`) is a bare
  `return s.store.DeleteTask(id)` — no cascade, no scan of other tasks'
  `depends_on` arrays. Confirms the ADR's "dangling ID" claim.
- **The staleness risk is worse than the ADR frames it — it's an active
  scheduler correctness bug, not just stale metadata.**
  `internal/runtime/scheduler/picker.go:257-281` uses `depends_on` to gate
  whether a task is eligible to run: it unmarshals the JSON array and, for
  each `depID`, calls `p.store.GetTask(depID)`; if that lookup errors
  (which it will, once the dependency task has been deleted), `allMet` is
  set `false` and the dependent task is skipped with
  `SkipReasonDepUnmet = "dep_unmet"` (`picker.go:22`). Since nothing ever
  prunes the stale ID, **a task whose dependency is later deleted becomes
  permanently, silently unschedulable** — reported forever as "waiting on
  an unmet dependency" when the dependency doesn't exist at all. This is a
  live deadlock bug today, independent of ENT-TASK or this decision; it's
  cited here because it materially raises the severity of "just document
  it and move on."
- **Precedent for the join-table shape already exists in this codebase.**
  `internal/persistence/sqlstore/migrations/005_tags.sql` did exactly this
  promotion for `tasks.tags`: created `tags` + `task_tags` (composite PK,
  `FOREIGN KEY ... ON DELETE CASCADE` both directions), then
  `ALTER TABLE tasks DROP COLUMN tags`. That means the *migration
  mechanics* for `depends_on` are lower-risk than the ADR's "bigger
  structural change" framing suggests in isolation — no full `tasks_new`
  rebuild is needed (unlike FK-002's sprint_id/project_id/epic_id
  promotion, which converts existing columns in place and does need one).
  A `task_dependencies(task_id, depends_on_task_id)` join table with
  `FOREIGN KEY (depends_on_task_id) REFERENCES tasks(id) ON DELETE CASCADE`
  is a known-good, already-proven pattern here.

### Why Option 1 over Option 2

1. **`ON DELETE CASCADE` fixes the deadlock bug as a side effect.** When a
   dependency task is deleted, the join-table row disappears automatically
   — the dependent task is no longer blocked on a phantom dependency on
   the next scheduling tick. Documenting the risk (Option 2) leaves the
   deadlock in place; it just tells callers it can happen.
2. **The migration itself is not the expensive part** — the proven
   tags→task_tags precedent de-risks the schema change specifically. What
   makes this bigger than ENT-TASK's scope is the number of *read/write
   call sites* that assume "one JSON string column":
   `internal/service/task.go` (Create/Update marshal), 
   `internal/service/task_validation.go` (unmarshal + existence check),
   `internal/persistence/sqlstore/tasks.go` (scan/insert), 
   `internal/mcpadapter/task_tools.go` (JSON-string encode/decode at the
   MCP boundary, lines ~325-327, 510, 541-542), and
   `internal/runtime/scheduler/picker.go` (the dependency-gating read
   path, which would move from JSON-unmarshal to a join-table query).
   Touching five layers across service/persistence/MCP/scheduler is
   unambiguously bigger than ENT-TASK's stated scope (exposing already-
   supported fields on `torque_task_create`), matching the ADR's own §6
   flag and this file's acceptance criteria.
3. **Matches this codebase's existing convention for real-FK promotions.**
   `tasks/phase-3-fk-migration/FK-001-orphan-backfill-cleanup.md` and
   `FK-002-promote-real-foreign-keys.md` already carve the
   sprint_id/project_id/epic_id promotion out of general entity work into
   a dedicated "Phase 3 — Real FK migration" track. The `depends_on`
   join-table promotion is a natural sibling to that phase (a plausible
   `FK-003`), not something that belongs bolted onto ENT-TASK's field-
   exposure work.
4. **Matches ADR-0004's own design principle #5** ("Relationships that are
   real are enforced as real — not checked in Go, unenforced in SQLite").

### Scope note — split required, not optional

Per the acceptance criteria: this is materially bigger than the rest of
ENT-TASK and should **not** be folded in. Recommend the orchestrator
create a new task file (suggested home: `tasks/phase-3-fk-migration/`,
alongside FK-001/FK-002, as a sibling FK-003-shaped task) scoped to:

- New migration: `task_dependencies(task_id, depends_on_task_id)` with
  composite PK and `ON DELETE CASCADE` on both FKs (CASCADE on
  `depends_on_task_id` prunes the edge when the dependency is deleted —
  fixing the picker.go deadlock; CASCADE on `task_id` prevents orphaned
  join rows when the dependent task itself is deleted, which is a second,
  smaller dangling-reference direction not called out in the ADR).
  `ALTER TABLE tasks DROP COLUMN depends_on` once the new path is live,
  following the exact `005_tags.sql` precedent.
- Update `TaskService` Create/Update/Get to read/write the join table
  instead of marshaling/unmarshaling a JSON column.
- Update `task_validation.go`'s existence check to query the join table
  (or keep it conceptually the same check against a different source).
- Update `internal/mcpadapter/task_tools.go`'s `depends_on` JSON-string
  encode/decode at the MCP boundary (the tool-facing contract — a JSON
  array of task IDs in the request/response — can likely stay unchanged
  even though the storage underneath changes; that's a plus for callers).
- Update `picker.go`'s dependency-gate read path to query the join table.
- Open sub-question for that task's scope (not decided here, flagging so
  it isn't missed): `depends_on` has no cycle detection today, unlike
  `parent_id` (`TaskService.validateParentID`,
  `internal/service/task.go:496-522`, which walks ancestors to reject
  cycles). Worth deciding in the split-out task whether a join table
  should get the same cycle guard, or whether that's explicitly deferred
  again.

### Interim note for ENT-TASK

Since the split-out task is unscheduled and may not land immediately,
ENT-TASK's `torque_task_create`/`update` docstrings may still want a
brief interim caveat about `depends_on` staleness (in the spirit of
Option 2's language) until the join-table task ships. That wording isn't
drafted here since Option 1 was chosen — but noting it so it isn't lost.
