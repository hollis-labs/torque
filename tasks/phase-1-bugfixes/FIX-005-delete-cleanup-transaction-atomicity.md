# FIX-005 — Wrap Sprint/Project/Epic delete-time task cleanup in a transaction

**Phase:** 1 — Locked bug fixes
**Status:** todo
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

- [ ] `DeleteSprint`/`DeleteProject`/`DeleteEpic` each run their
      reference-cleanup `UPDATE` and row `DELETE` inside one transaction.
- [ ] The `UPDATE`'s error is checked and propagated (via rollback +
      returned error), not discarded.
- [ ] A test forcing the `UPDATE` to fail proves the `DELETE` does not
      happen and no partial state is left behind.
- [ ] Full test suite passes.

## Out of scope

- Re-running FK-001's cleanup tool against production — that's an
  operational step, not code.
- Any change to FK-002 (real FK constraint promotion) — this fix is
  independent of and complementary to it; once FK-002 lands, `ON DELETE
  SET NULL` at the DB level makes the manual `UPDATE` redundant, but this
  fix should land regardless of FK-002's timing since it closes the gap
  immediately.

## Outcome

_(fill in when resolved)_
