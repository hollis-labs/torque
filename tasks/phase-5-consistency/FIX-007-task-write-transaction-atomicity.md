# FIX-007 — Wrap TaskService.Create/Update's multi-write paths in a transaction

**Phase:** 5 — Consistency sweep (spun out of the post-Phase-5 code-review pass)
**Status:** todo
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

- [ ] `TaskService.Create` with a `depends_on` list rolls back the task row
      if setting dependencies fails — no orphaned task row left behind.
- [ ] `TaskService.Update` with multiple changed fields (including tags
      and/or `depends_on`) rolls back all of them together if any one
      write fails — no partial-apply state.
- [ ] A forced-failure test (real constraint trigger, not a mock) proves
      the rollback for both `Create` and `Update`.
- [ ] Full test suite passes.

## Out of scope

- Any change to `TaskService.Delete` or the Sprint/Project/Epic delete
  paths — those are already fixed by FIX-005.
- Any change to `Create`/`Update`'s field validation logic — this is
  purely about write atomicity.

## Outcome

_(fill in when resolved)_
