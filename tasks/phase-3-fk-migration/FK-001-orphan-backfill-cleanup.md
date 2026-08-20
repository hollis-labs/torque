# FK-001 — Backfill/cleanup orphaned sprint_id/project_id/epic_id refs

**Phase:** 3 — Real FK migration
**Status:** todo
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

- [ ] A repeatable script/migration step exists that finds all three kinds
      of orphaned references.
- [ ] Orphans found are resolved (set to `NULL`) before FK-002 runs.
- [ ] Re-running the find-orphans query after cleanup returns zero rows.
- [ ] Document how many orphans were found and cleaned (a one-line note is
      enough) — useful context if FK-002's migration fails partway and
      someone needs to know whether cleanup actually ran.

## Out of scope

- Adding the actual `REFERENCES` constraint — that's FK-002.
- `depends_on` cleanup — separate, bigger structural question, see
  DEC-003.
