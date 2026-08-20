# DEC-001 — Pagination strategy: cursor vs offset

**Phase:** 0 — Decisions
**Status:** todo
**Depends on:** none
**Blocks:** PRIM-001 (and transitively every Phase 4 entity task)
**Source:** ADR-0004 §3 (Pagination), §6 (Open questions); audit "Proposed
Direction A"

## Summary

ADR-0004 states cursor/keyset pagination as the *target* but explicitly
declines to lock it: "this ADR states cursor as the target, not a hard
requirement" and lists it as an open question for the sequencing session.
This task makes that call before PRIM-001 is implemented, since every
downstream list-tool change depends on the shape decided here.

## The tradeoff, as stated in the ADR

- **Cursor/keyset**: correct under concurrent writes once `sort_by` is
  selectable — offset silently duplicates/skips rows as data changes between
  calls, and ties in the sort column make page boundaries nondeterministic
  without a stable tiebreaker (every list tool would break ties on `id`).
  More expensive to implement (opaque cursor encode/decode per sort key).
- **Offset + `total_count`**: cheaper — five stores (`tasks`, `collections`,
  `epics`, `sprints`, `projects`) already have working `Limit`/`Offset` at
  the SQL layer, just never wired through service/MCP (audit "Pagination"
  table). Correctness risk is real but may be acceptable for agent-driven
  list calls that are typically read-once, not paged deeply.

## Decision to make

Pick one:
1. **Cursor/keyset**, per-entity, tiebreak on `id`. Applies uniformly across
   all 7 list tools touched in Phase 4.
2. **Offset + total_count**, wiring the dead `Offset` fields that already
   exist. Cheaper, matches audit's "cheapest fix in the whole audit" framing.
3. **Hybrid**: offset now (fast, unblocks Phase 4), cursor as a later
   migration once sort lands everywhere and the correctness risk is proven
   to matter in practice.

## Acceptance criteria

- [ ] Decision recorded (in this file's Outcome section below, or as an ADR
      addendum) with the chosen strategy and one-paragraph rationale.
- [ ] If cursor is chosen: the cursor encoding scheme (opaque token — what's
      inside it, e.g. base64(last sort value + last id)) is specified enough
      for PRIM-001 to implement without re-deciding.
- [ ] If offset is chosen: confirm whether `total_count` (extra `COUNT(*)`
      query) or `has_more` (fetch `limit+1`, cheaper) is the metric — the
      ADR allows either ("or at minimum `has_more`").
- [ ] PRIM-001 unblocked.

## Out of scope

Implementation itself — that's PRIM-001. This task only decides the shape.

## Outcome

_(fill in when resolved)_
