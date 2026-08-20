# FK-002 — Promote sprint_id/project_id/epic_id to real FKs

**Phase:** 3 — Real FK migration
**Status:** todo
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

- [ ] Migration adds real FK constraints on all three columns.
- [ ] Deleting a sprint/project/epic that still has tasks referencing it
      now sets those tasks' respective column to `NULL` automatically
      (verified with a real delete, not just reading the schema).
- [ ] Existing Go-level existence checks at write time can stay (defense in
      depth) or be simplified — note which was chosen; not required to
      remove them, but flag if they're now fully redundant.
- [ ] Full test suite passes after migration (SQLite table rebuilds are a
      common source of subtle breakage — check any code that assumes
      column ordering or does raw SQL against `tasks` outside the
      `sqlstore` layer).

## Out of scope

- `depends_on` — separate, bigger structural change, see DEC-003. Not part
  of this migration.
