# FK-001 — Backfill/cleanup orphaned sprint_id/project_id/epic_id refs

**Phase:** 3 — Real FK migration
**Status:** done
**Depends on:** none (independent track, can run in parallel with Phase 2)
**Blocks:** FK-002
**Source:** ADR-0004 §3 (Real foreign keys), §4 (Task field reference)

## Summary

`tasks.sprint_id`, `tasks.project_id`, `tasks.epic_id` are plain `TEXT`
columns today with no `REFERENCES` clause — existence is checked in Go at
write time only, so a deleted sprint/project/epic can leave a dangling
reference SQLite won't stop. Before FK-002 can add a real constraint,
existing rows with stale/orphaned IDs need to be found and cleaned up —
the ADR explicitly flags this as "not solved here — flagged for the
implementer."

## Scope

- Write a query/script to find every `tasks` row where `sprint_id`,
  `project_id`, or `epic_id` is non-null but references a row that no
  longer exists in `sprints`/`projects`/`epics`.
- Decide and apply a cleanup strategy for what's found — most likely
  setting the dangling column to `NULL` (matching the target `ON DELETE SET
  NULL` behavior the constraint will enforce going forward), but confirm
  there isn't a better resolution (e.g. if orphans cluster around one
  specific deleted sprint, it might be worth a decision on whether that's
  expected/intentional data hygiene or a bug elsewhere).
- Run this against whatever environments matter (at minimum, confirm
  against a production/real data snapshot if one exists, not just a dev
  DB — orphans are unlikely to show up in a freshly-seeded dev instance).

## Acceptance criteria

- [x] A repeatable script/migration step exists that finds all three kinds
      of orphaned references.
- [x] Orphans found are resolved (set to `NULL`) before FK-002 runs.
- [x] Re-running the find-orphans query after cleanup returns zero rows.
- [x] Document how many orphans were found and cleaned (a one-line note is
      enough) — useful context if FK-002's migration fails partway and
      someone needs to know whether cleanup actually ran.

## Out of scope

- Adding the actual `REFERENCES` constraint — that's FK-002.
- `depends_on` cleanup — separate, bigger structural question, see
  DEC-003.

## Execution notes

**What was built.** A `torque fk-orphan-cleanup` CLI subcommand
(`cmd/torque/fk_orphan_cleanup.go`, registered in `cmd/torque/main.go`
alongside `cost-backfill`/`aar`, the existing precedent for this kind of
one-off data-maintenance command). It opens the configured DB the same way
`cost-backfill` does (`config.Load` → `appdb.Open` → `migrations.Run` →
`sqlstore.New`), so it honors `TORQUE_DB_PATH` and runs any pending schema
migrations first.

For each of `sprint_id`/`project_id`/`epic_id`, it:
1. Finds every distinct dangling value via `NOT EXISTS` against
   `sprints`/`projects`/`epics`, with a per-value task count.
2. Logs a report (top 3 dangling values shown, rest summarized), and emits
   an explicit `WARNING` line when one dangling id accounts for ≥50% of
   that column's orphans (with ≥3 total) — this is the "confirm nothing
   looks like a bug elsewhere" check from Scope: it doesn't change the
   cleanup behavior, but makes a suspicious single-deletion cluster visible
   in the log rather than silently nulling it alongside scattered staleness.
3. In one transaction, runs `UPDATE tasks SET <col> = NULL WHERE <col> IS
   NOT NULL AND NOT EXISTS (...)` for all three columns — matching FK-002's
   future `ON DELETE SET NULL` semantics. Tasks themselves are never
   touched/deleted, only the dangling column.

`--dry-run` reports steps 1–2 only, writes nothing. Default (no flag) does
all three steps. Re-running afterward is a no-op (step 1 finds zero rows),
satisfying "repeatable" and the re-run acceptance criterion directly.

Unit tests: `cmd/torque/fk_orphan_cleanup_test.go`
(`TestFindFKOrphans_NoOrphansInCleanData`,
`TestFindAndNullifyFKOrphans`) exercise the find/nullify/report functions
directly against an in-memory `sqlstore.Store`, including a clustered case
(4 tasks on one deleted sprint) and a scattered case (2 tasks on 2 different
deleted epics), asserting the `WARNING` fires only for the clustered one.

**Validation — no real dev/prod data was reachable, said plainly.** This
worktree is fully sandboxed (filesystem/git access outside the worktree is
blocked at the tool level), so there was no way to check for or read an
existing dev/prod Torque DB (`~/Library/Application Support/torque`, XDG
paths, etc.) as the Scope section asks for. No DB of any kind existed
inside the worktree either. Given the task's explicit fallback ("if none
exists, build/seed a minimal one to prove the query works, and say so
clearly"), a fresh SQLite DB was created via `torque fk-orphan-cleanup`
itself (which runs migrations on open) in the session scratchpad, then
seeded by hand with `sqlite3`:
- 1 healthy task with valid `sprint_id`/`project_id`/`epic_id`.
- 4 tasks all pointing at the same nonexistent sprint id (cluster case).
- 1 task pointing at a nonexistent project id.
- 2 tasks each pointing at a different nonexistent epic id (scattered case).

Results, run against the built `torque` binary:
- `--dry-run` found and reported all 7 orphaned references correctly
  (`sprint_id=4`, `project_id=1`, `epic_id=2`) and wrote nothing; the
  cluster `WARNING` fired only for the 4/4 sprint case, not the 1-per-id
  epic case, as designed.
- The real run cleaned exactly those 7 (`cleaned orphaned references:
  sprint_id=4 project_id=1 epic_id=2 (total=7)`), left the healthy task's
  three references untouched, and nulled the dangling columns without
  deleting any task rows.
- A follow-up `--dry-run` found zero orphans, confirming idempotency
  end-to-end (not just at the unit-test level).

So: **this ran successfully against a synthetic seeded DB proving the
tool's logic, not against any real dev/production dataset** — none was
available in this environment. Whoever runs this for real (per Scope,
"confirm against a production/real data snapshot if one exists") should
run `torque fk-orphan-cleanup --dry-run` first against that snapshot and
read the per-column counts/cluster warnings before running for real.

**Related finding, not fixed here (out of scope for this ticket).**
`DeleteSprint`/`DeleteProject`/`DeleteEpic`
(`internal/persistence/sqlstore/{sprints,projects,epics}.go`) already null
out referencing tasks' `sprint_id`/`project_id`/`epic_id` as part of a
normal delete — so orphans shouldn't accumulate from routine use of those
paths. But that cleanup runs as a separate, error-ignored
`s.db.Exec("UPDATE tasks SET ... WHERE ... = ?", id)` call *before* the row
`DELETE`, outside any shared transaction — e.g. `DeleteSprint`:
`s.db.Exec(...)` (return value discarded) then `s.db.Exec("DELETE FROM
sprints WHERE id = ?", id)`. If that first `UPDATE` ever fails (lock
contention, disk full, etc.) while the `DELETE` still succeeds, that's a
plausible real source of exactly this kind of orphan going forward — more
likely than a wholesale "delete forgot to clean up" bug. Worth a follow-up
to wrap those two statements in one transaction (or check/return the
`Exec` error) so this cleanup tool doesn't need to be periodically re-run
against production; not actioned here since it's a service-layer
correctness fix, not backfill/cleanup scope.

**Issues:** none blocking. `go build ./...` and `go test ./...` pass
(40/40 packages, `cmd/torque` included).
