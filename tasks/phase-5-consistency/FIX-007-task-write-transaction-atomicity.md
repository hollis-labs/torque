# FIX-007 — Wrap TaskService.Create/Update's multi-write paths in a transaction

**Phase:** 5 — Consistency sweep (spun out of the post-Phase-5 code-review pass)
**Status:** done
**Depends on:** none
**Blocks:** none
**Source:** High-effort code-review pass over the full session diff (`main`
vs `origin/main`), findings at `internal/service/task.go:407` and `:542`.
Same bug class as [FIX-005](FIX-005-delete-cleanup-transaction-atomicity.md),
which fixed the analogous issue in `DeleteSprint`/`DeleteProject`/
`DeleteEpic`.

## Summary

`TaskService.Create` and `TaskService.Update` each perform multiple,
independent, non-transactional store writes. If a later write in the
sequence fails, the earlier ones have already committed — there's no
rollback, so the caller gets an error but the store is left in a
partially-applied state.

- **`Create`** (`internal/service/task.go` ~line 407): `CreateTask` commits
  the task row, then a separate `SetTaskDependencies` call writes the
  `depends_on` join-table rows (FK-003). If `SetTaskDependencies` fails
  (lock contention, or a dependency task deleted in the TOCTOU window after
  `validateTaskWrites`' existence check trips the FK), `Create` returns an
  error — but a real task row now exists with none of its intended
  dependency edges, and the caller has no signal that a task was actually
  created.
- **`Update`** (`internal/service/task.go` ~line 542): `UpdateTask`,
  `SetTaskTags`, and `SetTaskDependencies` run as three independent calls.
  A multi-field update (e.g. title + `depends_on` in the same call) can
  partially apply: `UpdateTask` commits the title change, then
  `SetTaskDependencies` fails — the title is now updated but the
  dependency edges aren't, and a blind retry re-applies the title update
  against already-changed data.

## Scope

- Wrap `Create`'s `CreateTask` + `SetTaskDependencies` (when dependencies
  are actually being set) in a single transaction.
- Wrap `Update`'s `UpdateTask` + `SetTaskTags` (when tags are being
  changed) + `SetTaskDependencies` (when dependencies are being changed)
  in a single transaction.
- Follow FIX-005's pattern: check whether `internal/persistence/sqlstore`
  already has a transaction-helper (`beginWriteTx()`, used by FIX-005's
  `DeleteSprint`/`DeleteProject`/`DeleteEpic`) and reuse it rather than
  hand-rolling `Begin`/`Commit`/`Rollback`. This likely means adding
  transaction-aware variants of `CreateTask`/`SetTaskDependencies`/
  `UpdateTask`/`SetTaskTags` (i.e. methods that accept a `*sql.Tx` or the
  existing `WriteTx` type used elsewhere in this codebase) rather than
  changing their existing signatures, so other call sites aren't disturbed.
- Verify against a real forced failure (not just reading the code): a test
  that makes the second write in each sequence fail and confirms the first
  write was rolled back — same verification style FIX-005 used (a real
  SQLite trigger forcing a constraint failure, not a mock).

## Acceptance criteria

- [x] `TaskService.Create` with a `depends_on` list rolls back the task row
      if setting dependencies fails — no orphaned task row left behind.
- [x] `TaskService.Update` with multiple changed fields (including tags
      and/or `depends_on`) rolls back all of them together if any one
      write fails — no partial-apply state.
- [x] A forced-failure test (real constraint trigger, not a mock) proves
      the rollback for both `Create` and `Update`.
- [x] Full test suite passes.

## Out of scope

- Any change to `TaskService.Delete` or the Sprint/Project/Epic delete
  paths — those are already fixed by FIX-005.
- Any change to `Create`/`Update`'s field validation logic — this is
  purely about write atomicity.

## Outcome

`TaskService.Create`'s `CreateTask` + `SetTaskDependencies` (when `depends_on` is
set) and `TaskService.Update`'s `UpdateTask` + `SetTaskTags` (when tags change)
+ `SetTaskDependencies` (when `depends_on` changes) now each commit inside a
single transaction. A failure partway through rolls back everything already
written in that call — no orphaned task row, no partial-apply state.

## Execution notes

**Transaction pattern used.** `beginWriteTx()` (the private `s.db.Begin()`
wrapper FIX-005 used for `Delete*`) is unexported and only usable from
*within* `sqlstore` — but the transaction here has to span a call from the
*service* package into two-or-three separate store methods, so it can't be
used directly. The codebase already has an exported, cross-package answer to
exactly this shape of problem: `(s *Store) BeginWriteTx(ctx) (*WriteTx, error)`
(`internal/persistence/sqlstore/write_tx.go`), already used by
`TaskService.TransitionWithComment` to commit `TransitionTask` + `AddComment`
together. `WriteTx` already carried a `CreateTask` method (an exact mirror of
`Store.CreateTask`, just bound to `w.tx`); this task added the three missing
siblings — `WriteTx.UpdateTask`, `WriteTx.SetTaskTags`,
`WriteTx.SetTaskDependencies` — plus the service-layer call sites that use
them.

Rather than duplicating `Store.UpdateTask`'s ~170-line conditional
set-clause builder onto `WriteTx` verbatim (the way `WriteTx.CreateTask`
duplicates `Store.CreateTask` field-for-field — tolerable at ~30 lines, a
real drift risk at ~170), the three methods that needed new tx-variants
(`UpdateTask`, `SetTaskTags`, `SetTaskDependencies`) were refactored to share
one implementation each, parameterized over a small new unexported
`dbExecer` interface (`Exec(query string, args ...any) (sql.Result, error)`,
satisfied by both `*sql.DB` and `*sql.Tx`):

- `tasks.go`: `Store.UpdateTask` now delegates to `updateTaskExec(s.db, id, u)`;
  `WriteTx.UpdateTask` delegates to `updateTaskExec(w.tx, id, u)`. One copy of
  the field-mapping logic.
- `tags.go`: `Store.SetTaskTags` now begins its own tx (unchanged) and calls
  `setTaskTagsExec(tx, ...)` before committing; `WriteTx.SetTaskTags` calls
  `setTaskTagsExec(w.tx, ...)` directly (no nested transaction — it's already
  running inside the caller's `WriteTx`).
- `task_dependencies.go`: same split — `Store.SetTaskDependencies` begins/
  commits its own tx around `setTaskDependenciesExec`; `WriteTx.SetTaskDependencies`
  calls the same exec function against `w.tx`.

`CreateTask` was left untouched (both `Store.CreateTask` and the pre-existing
`WriteTx.CreateTask` are unchanged) since the tx-variant already existed and
worked correctly; no reason to touch stable, working code.

Service-layer call sites (`internal/service/task.go`):

- `Create`: when `len(input.DependsOn) > 0`, opens a `WriteTx`, calls
  `wtx.CreateTask(rec)` then `wtx.SetTaskDependencies(id, input.DependsOn)`,
  then `wtx.Commit()`. The no-`depends_on` path is untouched — a single
  `s.store.CreateTask(rec)` call, already atomic on its own. Subtodos and
  Tags (also written in `Create`) were deliberately left as separate,
  non-transactional calls after this block — out of this task's scope per
  the task doc (only `CreateTask` + `SetTaskDependencies` were named).
- `Update`: when `input.Tags == nil && input.DependsOn == nil`, falls
  straight through to the original single `s.store.UpdateTask(id, ...)` call
  (already atomic — one SQL statement, no transaction overhead added for the
  common case). Otherwise resolves tag slugs (a pure lookup/create step, no
  task-row side effects, so safe to run before opening the tx), opens a
  `WriteTx`, and runs `UpdateTask` + (conditionally) `SetTaskTags` +
  (conditionally) `SetTaskDependencies` inside it before `Commit()`.

Both blocks use `s.store.BeginWriteTx(context.Background())` — `Create`/
`Update` don't carry a `context.Context` parameter, and `BeginWriteTx`
already treats a nil ctx as `context.Background()` internally, so this
matches existing behavior for those (non-existent) callers while satisfying
the exported signature.

**Forced-failure tests.** Added two tests to
`internal/service/task_test.go`, mirroring FIX-005's style — a real SQLite
trigger, not a mock:

```sql
CREATE TRIGGER fail_task_dependency_insert
BEFORE INSERT ON task_dependencies
BEGIN
    SELECT RAISE(ABORT, 'forced failure for test');
END;
```

- `TestTaskCreateRollsBackOnDependencySetFailure`: creates a dependency
  target task, installs the trigger, peeks the next task ID via
  `svc.Store().NextTaskID()` (a pure read — safe to call twice in a
  single-threaded test with no intervening write), then calls
  `svc.Task.Create` with `DependsOn` set. Asserts `Create` returns an error
  *and* `GetTask(predictedID)` also errors with `sqlstore.ErrTaskNotFound` —
  i.e. the task row was never left behind.
- `TestTaskUpdateRollsBackOnDependencySetFailure`: creates a task, installs
  the trigger, then calls `svc.Task.Update` with both a title change and a
  `DependsOn` change. Asserts `Update` returns an error, the task's title is
  still the original (not the attempted new value), and
  `ListDependencyIDs` is empty — i.e. neither write stuck.

**Sanity-checked the tests are load-bearing**, same method FIX-005 used:
`git stash push` on the five changed non-test files (reverting to the
pre-fix, non-transactional code) while keeping the new tests, then re-ran
just those two tests. Both failed as expected against the reverted code:
- `TestTaskCreateRollsBackOnDependencySetFailure` failed at the
  `GetTask(predictedID)` assertion — `Create` did return an error (the
  trigger still fired), but the task row was found anyway, confirming the
  orphaned-row bug the task doc describes.
- `TestTaskUpdateRollsBackOnDependencySetFailure` failed on the title
  assertion — `unchanged.Title` came back `"updated title"` instead of
  `"original title"`, confirming the partial-apply bug.

`git stash pop` restored the fix afterward.

**Files touched:**
- `internal/persistence/sqlstore/tasks.go` — added `dbExecer` interface;
  refactored `Store.UpdateTask` to delegate to new `updateTaskExec`.
- `internal/persistence/sqlstore/tags.go` — refactored `Store.SetTaskTags` to
  delegate to new `setTaskTagsExec`.
- `internal/persistence/sqlstore/task_dependencies.go` — refactored
  `Store.SetTaskDependencies` to delegate to new `setTaskDependenciesExec`.
- `internal/persistence/sqlstore/write_tx.go` — added `WriteTx.UpdateTask`,
  `WriteTx.SetTaskTags`, `WriteTx.SetTaskDependencies`.
- `internal/service/task.go` — `Create` and `Update` now use `WriteTx` to
  wrap their multi-write paths per the Scope section above.
- `internal/service/task_test.go` — the two forced-failure rollback tests.

**Verification:** `go build ./...`, `go vet ./...`, and `go test ./...`
(full suite, every package, `-count=1`) all pass. The two new tests were
additionally verified to fail against the pre-fix code (see above).

**Issues/blockers:** none.
