# PRIM-004 — Archive primitive (`archived_at` + archive/unarchive)

**Phase:** 2 — Shared primitives
**Status:** todo
**Depends on:** none
**Blocks:** ENT-PROJECT, ENT-EPIC, ENT-SPRINT
**Source:** ADR-0004 §3 (Archive); audit "Cross-Cutting Findings §5
(Convenience tools)"

## Summary

Archive as a concept independent of `status` — archiving an epic isn't the
same fact as the epic being "done." Target: a real nullable `archived_at`
column on Project, Epic, Sprint, Issue, Plan, with `archive`/`unarchive`
tools, kept orthogonal to `status`. Collection already has the clean
reference pattern (`archive`/`unarchive` pair) to follow.

## Scope

- Migration: add nullable `archived_at` to Project, Epic, Sprint, and — per
  the ADR's target list — Issue and Plan (both are rows in `tasks`, so
  confirm whether this means a shared `tasks.archived_at` column or a
  narrower kind-scoped approach; Task itself explicitly keeps hard-delete +
  `abandoned` status as its own audit-preserving close path, archive is
  "optional parity there, not a gap" — don't force archive onto plain Task
  rows as a requirement of this migration).
- Generic `archive`/`unarchive` service methods, following Collection's
  existing pattern (`CollectionService`) as the reference implementation —
  read it first before building a new pattern from scratch.
- List filters should default to excluding archived rows unless an
  `include_archived` param is passed (check what Collection/Template
  already do here for consistency — Template already has an
  `IncludeArchived` filter param to mirror).

## Acceptance criteria

- [ ] `archived_at` migration lands for Project, Epic, Sprint (Issue/Plan
      scope confirmed per the note above before implementing).
- [ ] Generic archive/unarchive service methods exist, matching
      Collection's pattern.
- [ ] Archiving doesn't change `status` — verified by archiving a row and
      confirming its status field is untouched.
- [ ] List tools exclude archived rows by default; `include_archived=true`
      (or equivalent) surfaces them.

## Out of scope

- Wiring `archive`/`unarchive` MCP tools per-entity — that's each entity's
  Phase 4 task (this task builds the shared migration + service methods
  only).
- Task's own archive parity — explicitly optional per ADR, not required
  here.
