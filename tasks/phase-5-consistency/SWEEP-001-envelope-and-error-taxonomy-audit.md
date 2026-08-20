# SWEEP-001 — Envelope + error-taxonomy consistency pass across all 8 entities

**Phase:** 5 — Consistency sweep
**Status:** todo
**Depends on:** ENT-TASK, ENT-SUBTODO, ENT-COMMENT, ENT-PROJECT, ENT-EPIC,
ENT-SPRINT, ENT-ISSUE, ENT-PLAN (all Phase 4 tasks)
**Blocks:** none
**Source:** ADR-0004 §3 (Response envelope), §5 (per-entity design
principles: "one way to find things," "update is always a true partial
patch")

## Summary

Eight entities implementing the same primitives independently (across
different Phase 4 tasks, likely different sessions/agents) creates real
risk of subtle drift — one entity's `bulk_update` returning a slightly
different shape, one list tool's `meta` missing a field another has. This
task is the final pass confirming the whole surface actually landed
consistent, not just "each entity individually satisfies its own task
file."

## Scope

For each of Task, Subtodo, Comment, Project, Epic, Sprint, Issue, Plan,
verify:

- **List envelope**: every list-shaped tool's `data` is exactly
  `{items, meta: {returned, limit, total_count|has_more, next_cursor,
  hint?}}` — no entity-specific variation in field names or shape.
- **Bulk response shape**: every bulk tool (`bulk_update`, `bulk_delete`,
  `bulk_tag`, `bulk_transition` where applicable) returns exactly
  `{succeeded: [...], failed: [{id, error}]}`.
- **Error taxonomy**: every write path maps failures into
  `arg_invalid`/`not_found`/`conflict`/`domain`/`permission`/`internal`
  correctly — spot-check at least one FK-miss-style failure per entity to
  confirm it maps to `not_found`, not falling through to `internal` (the
  audit found this exact bug already present for Artifact; confirm none of
  the 8 in-scope entities have the same failure mode after Phase 4 landed).
- **Update semantics**: every `update`/`bulk_update` across all 8 entities
  uses presence-in-payload detection consistently — spot check for any
  entity that regressed to value-based detection (the bug FIX-001 fixed for
  Task) when it implemented its own update path.
- **"One way to find things"**: confirm no entity ended up with a stray
  parallel `_list`/`_search` pair that should have been merged (Task and
  Issue were explicitly merged in Phase 4; Comment's `list`/`search` split
  is the one deliberate exception — confirm nothing else drifted into a
  similar accidental split).
- **Docstrings**: spot-check that every list tool's docstring still
  accurately describes its actual default order after all the Phase 4
  changes (this is the same class of bug FIX-004 fixed — confirm it wasn't
  reintroduced).

## Acceptance criteria

- [ ] A single table (in this file's Outcome section, or a short follow-up
      doc) listing all 8 entities' list/bulk tool shapes side by side,
      confirming consistency or flagging drift found.
- [ ] Any drift found is either fixed as part of this task (if small) or
      spun back out into a new task file appended to this index (if it
      needs its own scoped work) — don't silently note drift without
      acting on it.
- [ ] Error-taxonomy spot-checks documented per entity (at least one
      `not_found`-shaped failure verified per entity).

## Out of scope

- New capability — this is a verification/consistency pass, not a place to
  add features that weren't already in scope for a Phase 4 task.

## Outcome

_(fill in when run — the consistency table goes here)_
