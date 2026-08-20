# FK-003 — Normalize `depends_on` into a real join table

**Phase:** 3 — Real FK migration
**Status:** done
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

- [x] `task_dependencies` join table exists with both `ON DELETE CASCADE`
      directions; `tasks.depends_on` column is dropped.
- [x] Deleting a task that other tasks depend on automatically removes the
      dependency edge (verified with a real delete, not just reading the
      schema) — confirm this actually unblocks the previously-deadlocked
      dependent task on the scheduler's next tick.
- [x] Deleting a task that itself has dependencies leaves no orphaned
      `task_dependencies` rows.
- [x] `torque_task_create`/`update`'s `depends_on` param behavior is
      unchanged from the caller's perspective (same JSON-array-of-IDs
      contract), even though storage changed underneath.
- [x] Existence validation on write still rejects a `depends_on` entry
      pointing at a nonexistent task.
- [x] Cycle detection decision made explicitly (built, or deferred with a
      documented reason) — not silently left as before.
- [x] Full test suite passes, including scheduler tests that exercise
      dependency gating (`internal/runtime/scheduler`).

## Out of scope

- `sprint_id`/`project_id`/`epic_id` FK promotion — that's FK-001/FK-002,
  a separate track.
- Any change to `parent_id`'s existing cycle-detection pattern beyond
  deciding whether `depends_on` should mirror it.

## Outcome

**DONE.** `depends_on` is now backed by a real `task_dependencies` join
table with `ON DELETE CASCADE` in both directions. The scheduler deadlock
described in DEC-003 (a deleted dependency permanently and silently blocking
its dependent) is fixed and covered by an end-to-end regression test. Cycle
detection was built, not deferred — see the decision writeup below.

## Execution notes

### Migration

`internal/persistence/sqlstore/migrations/027_task_dependencies.sql`,
following `005_tags.sql` as the template per the task's Precedent section:

```sql
CREATE TABLE task_dependencies (
  task_id            TEXT NOT NULL,
  depends_on_task_id TEXT NOT NULL,
  sort_order         INTEGER NOT NULL,
  created_at         TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, depends_on_task_id),
  FOREIGN KEY (task_id)            REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (depends_on_task_id) REFERENCES tasks(id) ON DELETE CASCADE
);
CREATE INDEX idx_task_dependencies_depends_on_task_id ON task_dependencies(depends_on_task_id);
```

Then a **backfill** (not present in 005_tags.sql, added here because
`depends_on` — unlike tags at the time of migration 005 — has live
production data): existing `tasks.depends_on` JSON arrays are expanded via
`json_each` into `task_dependencies` rows, preserving array order into
`sort_order`. Entries pointing at a task ID that no longer exists are
dropped by the backfill's own `WHERE je.value IN (SELECT id FROM tasks)`
filter rather than carried forward — those are exactly the dangling
references this migration exists to stop producing, and the
`depends_on_task_id` FK would reject them anyway. Verified the `json_each`
table-valued function works against this repo's actual driver
(`modernc.org/sqlite`) and that filtering NULL/empty/`[]` rows via a
subquery *before* the `json_each` join avoids any "malformed JSON" error
on rows that never had valid JSON to begin with. `ALTER TABLE tasks DROP
COLUMN depends_on` runs last, per the 005 precedent.

Migration + schema shape is covered by new assertions in
`internal/persistence/sqlstore/migrations/migrate_test.go` (table/column
existence, legacy column drop, composite-PK uniqueness, index existence).

### Cycle detection: built, not deferred

The task's Scope section flagged this as an explicit either/or decision.
**Decision: build it**, mirroring `TaskService.validateParentID`
(`internal/service/task.go`) but adapted for a DAG instead of a tree.

Rationale:

1. This task's entire premise is fixing a scheduler deadlock caused by an
   unenforced `depends_on` invariant. A mutual/circular dependency
   (A depends_on B, B depends_on A) is a second, self-inflicted flavor of
   exactly that deadlock class — and `ON DELETE CASCADE` does nothing for
   it, since neither task in a cycle is ever deleted. Fixing one deadlock
   vector while knowingly leaving a sibling one unaddressed, with no
   documented reason, didn't seem like a real decision — it read as leaving
   the gap ambiguous, which the task explicitly asked me not to do.
2. The infrastructure this needs (`ListTaskDependencyIDs`) was already
   being added for the picker fix and the MCP/HTTP read paths, so reusing
   it for cycle detection was marginal incremental cost, not a new
   subsystem.
3. It keeps `depends_on` consistent with the codebase's existing
   `parent_id` convention instead of introducing an asymmetry (one
   relationship cycle-safe, the sibling relationship not) with no stated
   rationale.

Implementation (`TaskService.validateDependsOnCycle`,
`internal/service/task.go`): unlike `parent_id` (a tree — one parent per
node, so a linear ancestor walk suffices), `depends_on` is a many-to-many
DAG (a task can depend on several others; several tasks can share a
dependency), so this is a DFS from each candidate dependency's own edges,
with a visited-set (handles diamond-shaped graphs without repeat work) and
a 256-hop cap (mirrors `validateParentID`'s guard). It only runs on
**Update**, not Create: a brand-new task has no ID until after
`NextTaskID` runs, so nothing existing could already hold an edge pointing
at it — a cycle is structurally impossible at create time, exactly the
same reasoning `validateParentID` already uses to skip its own check on
Create.

### Files touched

- `internal/persistence/sqlstore/migrations/027_task_dependencies.sql` — new migration.
- `internal/persistence/sqlstore/migrations/migrate_test.go` — schema-shape assertions for 027.
- `internal/persistence/sqlstore/task_dependencies.go` — new `SetTaskDependencies` / `ListTaskDependencyIDs` store methods (mirrors `tags.go`).
- `internal/persistence/sqlstore/tasks.go` — dropped `DependsOn` from `TaskRecord`/`TaskUpdate`, `taskSelectCols`, `scanTask`, `CreateTask`, `UpdateTask`.
- `internal/persistence/sqlstore/write_tx.go` — same drop in `WriteTx.CreateTask`, which mirrors `Store.CreateTask` field-for-field (not in the task's file list, but would not compile / would silently diverge otherwise).
- `internal/service/task.go` — `Create`/`Update` write through the join table instead of marshaling JSON; added `validateDependsOnCycle` and `ListDependencyIDs`; fixed a latent bug in `Update` where setting `Tags` caused an early `return`, skipping anything after it (now both `Tags` and `DependsOn` always apply).
- `internal/service/task_validation.go` — `extractUpdateFields` reads `TaskUpdateInput.DependsOn` (`*[]string`) directly; no more JSON unmarshal for this field.
- `internal/runtime/scheduler/picker.go` — **the deadlock fix**: dependency gating now queries `ListTaskDependencyIDs` instead of unmarshaling `task.DependsOn`. `SkipReasonDepMalformed` is kept defined (never emitted) rather than deleted, since malformed JSON is now structurally impossible and the constant is documented as load-bearing for log-grep key stability.
- `internal/runtime/scheduler/scheduler.go` — `buildJob` populates `job.DependsOn` (informational context only — gating already happened in `Picker.Pick` before dispatch) via the same store method; best-effort on lookup failure.
- `internal/mcpadapter/task_tools.go` — `depends_on` create/update now go through `reqStrSlice` (like `tags`) instead of a raw `reqStr` + `json.Unmarshal`; `taskWithTags` gained a `DependsOn []string` field (was previously absent from the MCP-visible shape except as the raw `TaskRecord.DependsOn` `sql.NullString`, which rendered as `{String,Valid}` — now a clean JSON array, a strict improvement to the response shape, same as `Tags`).
- `internal/httpserver/tasks.go`, `issues.go`, `plans.go`, `templates.go` — `taskJSON` takes an explicit `dependsOn []string` param now (previously read `t.DependsOn` internally); every call site updated to fetch it via `ListDependencyIDs` alongside tags. `TaskUpdateInput` now carries `DependsOn: req.DependsOn` explicitly instead of routing through the store-level `TaskUpdate.DependsOn`.
- Tests updated for the new shape: `internal/service/task_test.go`, `internal/service/task_validation_test.go` (one test — malformed-JSON-in-update — removed outright since that failure mode no longer exists at the service layer; still covered at the MCP boundary), `internal/mcpadapter/task_tools_test.go`, `internal/runtime/scheduler/{picker_test,picker_project_test,picker_unmet_dep_test,build_job_test}.go`.
- New: `internal/runtime/scheduler/picker_dep_cascade_test.go` — the deadlock-fix regression test (see below).

### Deadlock-fix verification

`internal/runtime/scheduler/picker_dep_cascade_test.go`,
`TestPickerDependencyCascadeOnDelete`, run against a real `*sqlstore.Store`
(foreign keys ON):

1. Create task `CW-DEL-DEP` (manual, so it never self-dispatches) and
   `CW-DEL-DEPENDENT`; link them with `SetTaskDependencies`.
2. `Pick()` — assert `CW-DEL-DEPENDENT` is skipped with
   `SkipReasonDepUnmet` (dependency exists, not done). This reproduces the
   pre-fix "blocked" state.
3. `store.DeleteTask("CW-DEL-DEP")` — the exact action that used to strand
   a JSON-array ID forever.
4. Assert `ListTaskDependencyIDs("CW-DEL-DEPENDENT")` is now empty —
   confirms the join row was actually pruned by `ON DELETE CASCADE`, not
   just that `Pick` happens to pass for some unrelated reason.
5. `Pick()` again — assert `CW-DEL-DEPENDENT` **is now picked**, with no
   restart, no manual cleanup, no other intervention. This is the
   "unblocks on the next scheduling pass" acceptance criterion.

A sibling test, `TestPickerDependencyCascadeOnDependentDelete`, confirms
the second, smaller direction from the Scope section: deleting the
*dependent* task itself leaves no orphaned `task_dependencies` row (`ON
DELETE CASCADE` on `task_id`).

All existing dependency-gating tests
(`picker_test.go`/`picker_project_test.go`/`picker_unmet_dep_test.go`) were
ported from constructing `TaskRecord{DependsOn: sql.NullString{...}}`
literals to `store.SetTaskDependencies(...)` calls and continue to pass
unmodified in behavior.

### Test results

`go build ./...` — clean. `go test ./...` (full suite, `-count=1`) — all
packages pass, including `internal/runtime/scheduler` (19.7s, the
deadlock-fix suite), `internal/service`, `internal/persistence/sqlstore`,
`internal/persistence/sqlstore/migrations`, `internal/mcpadapter`, and
`internal/httpserver`.

### Known limitations / follow-ups

- The MCP-facing `depends_on` response shape changed from a mangled
  `{String,Valid}` `sql.NullString` echo to a clean JSON array (see
  `task_tools.go` note above). This is a response-shape improvement, not a
  request-contract break — the acceptance criterion ("same
  JSON-array-of-IDs contract" for the *write* side) holds — but any caller
  that was reading the old malformed read-side shape will see a different
  (better) shape now.
- `validateDependsOnCycle` does not currently cap the total number of
  distinct nodes visited across all candidates in one call, only hops per
  DFS branch (256, matching `validateParentID`) plus the natural
  dedup from the visited-set. For pathological fan-out graphs this is a
  theoretical, not observed, cost concern; not addressed here since it
  mirrors the existing `parent_id` guard's own scope.
