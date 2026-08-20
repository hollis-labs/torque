# FIX-005 — Wrap Sprint/Project/Epic delete-time task cleanup in a transaction

**Phase:** 1 — Locked bug fixes
**Status:** done
**Depends on:** none
**Blocks:** none (independent hygiene fix; not required for any Phase 4
entity task, but should land before FK-002 promotes these columns to real
FKs so both cleanup paths agree)
**Source:** Found during
[FK-001](../phase-3-fk-migration/FK-001-orphan-backfill-cleanup.md)'s
execution, flagged as out of that task's scope (backfill/cleanup of
*existing* orphans, not a fix to how new ones could still occur).

## Summary

`DeleteSprint`/`DeleteProject`/`DeleteEpic`
(`internal/persistence/sqlstore/{sprints,projects,epics}.go`) already null
out referencing tasks' `sprint_id`/`project_id`/`epic_id` on delete — but
that cleanup runs as a separate, **error-ignored**
`s.db.Exec("UPDATE tasks SET ... WHERE ... = ?", id)` call, outside any
shared transaction with the row's own `DELETE`. E.g. `DeleteSprint`:
`s.db.Exec(...)` (return value discarded) then a second
`s.db.Exec("DELETE FROM sprints WHERE id = ?", id)`. If the `UPDATE` ever
fails (lock contention, disk full, etc.) while the `DELETE` still
succeeds, that's a plausible real source of the exact orphaned-reference
problem FK-001's cleanup tool exists to fix — meaning FK-001's tool could
need periodic re-running against production instead of being a one-time
backfill.

## Scope

- In each of `DeleteSprint`, `DeleteProject`, `DeleteEpic`
  (`internal/persistence/sqlstore/sprints.go`, `projects.go`, `epics.go`):
  wrap the reference-nulling `UPDATE` and the row's own `DELETE` in a
  single `*sql.Tx` (begin/commit/rollback-on-error), and check/return the
  `UPDATE`'s error instead of discarding it.
- Check whether `internal/persistence/sqlstore` has an existing
  transaction-helper pattern used elsewhere in the store layer (there
  likely is one, given the codebase's general care around this) and reuse
  it rather than hand-rolling `Begin`/`Commit`/`Rollback` three times.
- Verify against a real failure path: force the `UPDATE` to fail in a test
  (e.g. via a mock/wrapped DB, or by triggering a constraint violation) and
  confirm the `DELETE` doesn't proceed and the whole operation rolls back
  cleanly with an error surfaced to the caller.

## Acceptance criteria

- [x] `DeleteSprint`/`DeleteProject`/`DeleteEpic` each run their
      reference-cleanup `UPDATE` and row `DELETE` inside one transaction.
- [x] The `UPDATE`'s error is checked and propagated (via rollback +
      returned error), not discarded.
- [x] A test forcing the `UPDATE` to fail proves the `DELETE` does not
      happen and no partial state is left behind.
- [x] Full test suite passes.

## Out of scope

- Re-running FK-001's cleanup tool against production — that's an
  operational step, not code.
- Any change to FK-002 (real FK constraint promotion) — this fix is
  independent of and complementary to it; once FK-002 lands, `ON DELETE
  SET NULL` at the DB level makes the manual `UPDATE` redundant, but this
  fix should land regardless of FK-002's timing since it closes the gap
  immediately.

## Outcome

`DeleteSprint`, `DeleteProject`, and `DeleteEpic` now run their reference-nulling
`UPDATE`(s) and row `DELETE` inside a single transaction, with the `UPDATE`'s
error checked and propagated via rollback instead of discarded.

## Execution notes

**Transaction pattern used.** Reused the existing private helper
`(s *Store) beginWriteTx() (*sql.Tx, error)` (`internal/persistence/sqlstore/store.go:93`,
a thin wrapper over `s.db.Begin()`), which is already the established idiom
for scoped read/modify/write in this store package — see
`AddTaskToCollection` and `transitionTaskTx` in `collections.go`/`tasks.go`
for prior art. Did **not** reach for the heavier `BeginWriteTx`/`WriteTx`
type in `write_tx.go` — that helper is purpose-built for the
runtime-state/scheduler path (accumulates `afterCommit` hooks for
transition-event fan-out, uses `txutil.BeginImmediate` for the
writer-lock-at-BEGIN contract) and isn't used by any other CRUD-style
store method; `beginWriteTx()` is the right-sized tool here.

Each `Delete*` method now follows:
```go
tx, err := s.beginWriteTx()
if err != nil {
    return err
}
defer tx.Rollback() // no-op after Commit (sql.ErrTxDone swallowed by *sql.Tx)

if _, err := tx.Exec("UPDATE ... SET ... = NULL WHERE ... = ?", id); err != nil {
    return err // deferred Rollback undoes nothing (this was the first/only write so far)
}
// ...additional reference-nulling UPDATEs/DELETEs for DeleteProject...

result, err := tx.Exec("DELETE FROM <table> WHERE id = ?", id)
if err != nil {
    return err
}
n, _ := result.RowsAffected()
if n == 0 {
    return fmt.Errorf("<entity> %s not found", id)
}
return tx.Commit()
```

`DeleteProject` had four separate error-ignored statements before the final
`DELETE` (tasks UPDATE, sprints UPDATE, epics UPDATE, `project_artifacts`
DELETE) — all four now run inside the same transaction with checked errors,
not just the first one, since the acceptance criteria ("no partial state
left behind") applies to the whole cleanup chain, not only the first
statement.

**Test proving rollback works.** Added one test per method in
`sprints_test.go` / `epics_test.go` / `projects_test.go`
(`TestDeleteSprintRollsBackOnTaskCleanupFailure`,
`TestDeleteEpicRollsBackOnTaskCleanupFailure`,
`TestDeleteProjectRollsBackOnPartialCleanupFailure`). Each forces a real
constraint failure — not a mock — by installing a SQLite `BEFORE UPDATE ...
BEGIN SELECT RAISE(ABORT, ...); END` trigger on the column the
reference-nulling `UPDATE` touches (test stores are real file-backed SQLite
via `modernc.org/sqlite`, so triggers are genuine engine behavior, not a
stub). The project test targets the *second* statement in the chain (the
sprints `UPDATE`, which runs after the tasks `UPDATE` already succeeded) to
prove the whole transaction rolls back, not just the failing statement.

Each test asserts, after the forced failure:
- `DeleteX` returns a non-nil error.
- `GetX` still finds the row (the `DELETE` never ran / was rolled back).
- The referencing row(s) (task, and for the project case, sprint) still
  carry their original `sprint_id`/`epic_id`/`project_id` — i.e. the
  reference-nulling `UPDATE` was fully undone, including one that had
  already committed to the transaction (but not yet to the DB) before the
  failing statement ran.

Sanity-checked the tests are load-bearing: reverted `sprints.go` to its
pre-fix (error-ignored, non-transactional) form via `git stash` and
re-ran `TestDeleteSprintRollsBackOnTaskCleanupFailure` — it failed with
`Error: An error is expected but got nil`, confirming the test would have
caught the original bug. Restored the fix via `git stash pop` afterward.

**Files touched:**
- `internal/persistence/sqlstore/sprints.go` — `DeleteSprint`
- `internal/persistence/sqlstore/projects.go` — `DeleteProject`
- `internal/persistence/sqlstore/epics.go` — `DeleteEpic`
- `internal/persistence/sqlstore/sprints_test.go` — rollback test
- `internal/persistence/sqlstore/epics_test.go` — rollback test
- `internal/persistence/sqlstore/projects_test.go` — rollback test

**Verification:** `go build ./...` and `go test ./...` both pass (full
suite, all packages).

**Issues/blockers:** none. No overlap encountered with the parallel FK
work in `tasks.go`/`migrations/`.
