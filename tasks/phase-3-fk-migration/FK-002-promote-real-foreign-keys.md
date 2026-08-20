# FK-002 — Promote sprint_id/project_id/epic_id to real FKs

**Phase:** 3 — Real FK migration
**Status:** done
**Depends on:** FK-001 (orphans must be cleaned before the constraint can
be added)
**Blocks:** none directly (independent of Phase 4 entity work — those
filters already work at the Go level without this)
**Source:** ADR-0004 §3 (Real foreign keys)

## Summary

Promote `tasks.sprint_id`, `tasks.project_id`, `tasks.epic_id` from plain
`TEXT` to real `REFERENCES ... ON DELETE SET NULL` columns, matching the
existing `parent_id`/`collection_id` pattern already in the schema.

## Scope

- SQLite requires a full table rebuild to add this — the codebase already
  has this pattern; look at an existing `tasks_new` migration (e.g.
  `025_kind_issue.sql`) as the template rather than inventing a new
  rebuild approach.
- Add `REFERENCES sprints(id) ON DELETE SET NULL`,
  `REFERENCES projects(id) ON DELETE SET NULL`,
  `REFERENCES epics(id) ON DELETE SET NULL` to the three columns.
- Confirm `parent_id`/`collection_id`'s existing `ON DELETE SET NULL`
  behavior as the reference for exact SQL syntax already in use in this
  codebase.
- Verify the migration runs cleanly on a DB that already went through
  FK-001's cleanup (test this — don't assume FK-001 ran everywhere).

## Acceptance criteria

- [x] Migration adds real FK constraints on all three columns.
- [x] Deleting a sprint/project/epic that still has tasks referencing it
      now sets those tasks' respective column to `NULL` automatically
      (verified with a real delete, not just reading the schema).
- [x] Existing Go-level existence checks at write time can stay (defense in
      depth) or be simplified — note which was chosen; not required to
      remove them, but flag if they're now fully redundant.
- [x] Full test suite passes after migration (SQLite table rebuilds are a
      common source of subtle breakage — check any code that assumes
      column ordering or does raw SQL against `tasks` outside the
      `sqlstore` layer).

## Out of scope

- `depends_on` — separate, bigger structural change, see DEC-003. Not part
  of this migration.

## Execution notes

**What was built.** `internal/persistence/sqlstore/migrations/028_task_fk_constraints.sql`
rebuilds `tasks` (SQLite can't `ALTER` a column to add `REFERENCES`) following
the exact `tasks_new` → copy → `DROP` → `RENAME` pattern already used by
`025_kind_issue.sql`/`024_default_executor_cli.sql`, reproducing every
existing column verbatim (including `launch_profile`, added by `026` via
plain `ALTER TABLE` — folded into the rebuilt column list here) and every
existing index. The only schema change: `sprint_id`/`project_id`/`epic_id`
each gain `REFERENCES <table>(id) ON DELETE SET NULL`, matching
`tasks.parent_id`'s existing `TEXT REFERENCES tasks(id) ON DELETE SET NULL`
(added in `013_task_parent_id.sql`) — confirmed to be the real precedent for
this exact syntax. One correction to the ticket's premise:
`collection_id` (`020_collections.sql`) is `REFERENCES collections(id)` with
**no** `ON DELETE` clause (SQLite default `NO ACTION`), not `ON DELETE SET
NULL` — `parent_id` is the only existing `ON DELETE SET NULL` precedent in
this schema.

**Verification — synthetic DB, not real dev/prod data (said plainly, same as
FK-001).** No dev/prod Torque DB was reachable in this sandboxed worktree.
Built the `torque` binary, ran `torque fk-orphan-cleanup --dry-run` against a
throwaway `TORQUE_DB_PATH` to get a fresh DB migrated through `027` (pre-028),
then hand-seeded via `sqlite3`: a healthy task with valid
sprint/project/epic refs, 4 tasks clustered on one deleted sprint id, 1 task
on a deleted project id, 2 tasks scattered across two deleted epic ids (7
orphans total — mirrors FK-001's own fixture shape). Ran the full sequence
end to end:
1. **Negative path** — copied the seeded (still-orphaned) DB and ran the
   *post-028* `torque` binary directly (skipping cleanup on purpose): the
   migration's post-write `PRAGMA foreign_key_check` correctly caught it
   (`foreign key check after 028_task_fk_constraints.sql: tasks row 2
   references missing sprints (fk 5)`) and the process exited non-zero.
2. **`torque fk-orphan-cleanup` (FK-001) against the same seeded DB**, using
   a binary built *without* migration 028 (simulating the real deploy order:
   FK-001's cleanup binary ships and runs before the FK-002 binary lands) —
   `--dry-run` reported the exact 4/1/2 split with the cluster `WARNING`
   firing only for the 4-task sprint cluster, then the real run cleaned all
   7, then a follow-up `--dry-run` confirmed zero orphans remained.
3. **Positive path** — ran the post-028 `torque` binary against the now-clean
   DB: migration applied cleanly, `.schema tasks` confirmed all three
   `REFERENCES ... ON DELETE SET NULL` clauses landed.
4. **Real-delete verification** (not just schema-reading, per acceptance
   criterion): via raw `sqlite3` with `PRAGMA foreign_keys=ON` (matching what
   `sqlstore.New` sets for the app itself, `internal/persistence/sqlstore/store.go:73`)
   — `DELETE FROM sprints/projects/epics WHERE id IN (...)` against a task
   with all three refs populated flipped all three columns to `NULL`
   automatically, with zero application code involved. A second, isolated
   test confirmed the constraint is **column-selective**: deleting only the
   referenced sprint nulled `sprint_id` alone, leaving `project_id`/`epic_id`
   (still-valid refs) untouched.

Whoever runs this against a real DB should run `torque fk-orphan-cleanup
--dry-run` (FK-001) first, per FK-001's own notes — the migration's
`foreign_key_check` will now abort *and roll back* (see next section) rather
than apply over stale data, but a clean run first avoids the surprise
entirely.

**Bug found and fixed: `migrations.Run`'s FK check ran after commit, not
before.** Testing the negative path above (semantic-only apply, orphans
seeded via raw `sqlite3` since `store.CreateTask` now refuses them — see
below) surfaced that `internal/persistence/sqlstore/migrations/migrate.go`
called `checkForeignKeys` **after** `tx.Commit()` had already landed the
migration's schema change and its `schema_migrations` row. So the *first*
run against orphaned data returned an error (good) but left the DB with the
new `REFERENCES` schema committed, `028_task_fk_constraints.sql` marked
applied, and the 7 dangling rows sitting there unmodified — a genuinely
broken half-state a caller could easily miss, since the process still exited
non-zero and looked like a clean failure. This is pre-existing shared
migration-runner infra (not FK-002-specific), but was never exercised before
because no prior migration's own DML/DDL could actually trip its own
`foreign_key_check`. Fixed by moving `checkForeignKeys` to run *inside* the
still-open transaction, before `tx.Commit()` (`PRAGMA foreign_key_check`
reports violations regardless of the `foreign_keys` pragma's on/off state,
so it works fine there) — on violation we now `tx.Rollback()` instead of
committing. Re-ran the negative-path test after the fix: `schema_migrations`
stays at `027`, `tasks.sprint_id` schema is unchanged (`TEXT`, no
`REFERENCES`), and all 7 orphan rows are untouched — a clean, fully-reverted
failure. `internal/persistence/sqlstore/migrations/migrate_test.go` (which
exercises the happy path across all 28 migrations, including idempotent
re-run) still passes unchanged.

**Test suite fallout — all fixed, full suite green (40/40 packages).**
`go test ./...` initially surfaced 23 failures, all `FOREIGN KEY constraint
failed`, in three places — each is test fixtures that previously relied on
`sprint_id`/`project_id`/`epic_id` being unconstrained plain strings, now
correctly rejected by the new constraint:
- `cmd/torque/fk_orphan_cleanup_test.go` — `TestFindAndNullifyFKOrphans`'s
  `seedFKOrphanTask` helper plants a deliberately-dangling reference via
  `store.CreateTask`; that's now structurally impossible with the constraint
  enforced (which is the point of FK-002). Fixed by toggling
  `PRAGMA foreign_keys = OFF` around the raw insert in the helper (relying on
  `sqlstore.New`'s `SetMaxOpenConns(1)` for SQLite so the toggle reliably
  hits the one connection `CreateTask` will use), restoring it after — this
  is now the only way to simulate the legacy/pre-migration orphaned data this
  tool exists to clean up.
- `internal/runtime/scheduler/picker_test.go` (`setupPickerStore`, shared by
  `picker_project_test.go`, `picker_project_scope_filter_test.go`,
  `picker_unmet_dep_test.go`, `picker_test.go` — 21 of the 23 failures) and
  `internal/runtime/scheduler/cost_test.go` (`setupCostStore`,
  `TestCostTrackerSprintTotal`) — these tests use synthetic project/sprint id
  strings (`"PRJ-A"`, `"sprint-1"`, etc.) purely as grouping/allowlist keys;
  confirmed neither `Picker` (`picker.go`) nor `CostTracker.SprintTotal`
  (`cost.go:89-95`) ever joins against `projects`/`sprints` — they only
  filter/group by the string column value. Disabled `foreign_keys` in both
  shared setup helpers rather than seeding a real project/sprint row per
  literal ID scattered across ~15 test functions.
- `internal/e2e/agent_boot/plan_execute_test.go` (3 failures, all through the
  shared `createPlanTask` fixture) — unlike the above, this really is
  testing a realistic plan-task shape end-to-end, so fixed it properly:
  `createPlanTask` now creates a `PRJ-SMOKE` project row before the task that
  references it.

`go build ./...` and `go vet ./...` are clean. `go test ./...`: **40/40
packages pass**, zero failures.

**Go-level existence checks — kept, and NOT fully redundant (per acceptance
criterion, flagging rather than removing).** Researched the write path in
full (`internal/service/task.go`, `internal/persistence/sqlstore/tasks.go`,
HTTP/MCP error mapping):
- The only existing checks are in `TaskService.Create` (`task.go:226-261`,
  inlined, calling `s.store.GetSprint/GetProject/GetEpic`) — no
  `validateSprintID`-style helpers exist. **`TaskService.Update` has no
  equivalent check at all** — `sprint_id`/`project_id`/`epic_id` flow
  straight from HTTP (`internal/httpserver/tasks.go:735-743`) and MCP
  (`internal/mcpadapter/task_tools.go:557-568`) into
  `sqlstore.UpdateTask` unchecked. So the new DB constraint isn't "defense in
  depth" for Update — it's the *only* safety net there today, both for
  rejecting a bogus reference and (via `ON DELETE SET NULL`) for
  auto-cleaning references to entities deleted after the task was written.
- For Create, the Go check is still worth keeping for UX, not redundancy:
  neither `sqlstore.CreateTask` (`tasks.go:259-292`) nor `UpdateTask`
  (`tasks.go:429-616`) inspect the error returned by `Exec` — a raw FK
  violation propagates as-is. Traced up: HTTP's `writeError` only
  special-cases `*service.ValidationError` (422); anything else, including a
  raw `FOREIGN KEY constraint failed` string, becomes a 500 with that raw
  driver text in the body (`internal/httpserver/tasks.go:632-640`,
  `762-769`). MCP's `mapServiceError` (`internal/mcpadapter/errors.go:164-224`)
  has a richer typed/substring taxonomy but nothing matches a raw FK error
  message, so it falls through to `Default: internal` — silently logged
  server-side, caller just sees `code=internal`. ADR-0004 itself flags this
  exact fallthrough pattern as a bug class to fix
  (`docs/adr/0004-mcp-data-entity-agent-ergonomics.md:160-163`).
- **Decision: kept as-is, not simplified/removed.** Flagging two follow-ups
  rather than fixing here (out of scope for a schema migration): (a) add the
  missing existence check to `TaskService.Update` for parity with Create's
  friendly error, and/or (b) translate FK-constraint-violation errors in
  `CreateTask`/`UpdateTask` (or the HTTP/MCP error-mapping layers) into the
  existing `arg_invalid`/422 taxonomy instead of leaking a raw 500/`internal`.

**Related, not fixed here.** FK-001's own execution notes already flagged
that `DeleteSprint`/`DeleteProject`/`DeleteEpic`
(`internal/persistence/sqlstore/{sprints,projects,epics}.go`) null out
referencing tasks via a separate, error-ignored `Exec` *before* the row
`DELETE`, outside any shared transaction — that's now redundant with the new
`ON DELETE SET NULL` constraint (which fires atomically as part of the same
`DELETE` statement) but harmless to leave; not touched here since it's a
service-layer change outside this migration's scope.

**Issues:** none blocking.
