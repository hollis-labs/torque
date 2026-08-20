# PRIM-001 — Pagination + response envelope primitive

**Phase:** 2 — Shared primitives
**Status:** todo
**Depends on:** DEC-001 (pagination strategy must be decided first)
**Blocks:** ENT-TASK, ENT-COMMENT, ENT-PROJECT, ENT-EPIC, ENT-SPRINT,
ENT-ISSUE, ENT-PLAN
**Source:** ADR-0004 §3 (Pagination, Response envelope); audit "Cross-Cutting
Findings §1 (Pagination)", "Proposed Direction A"

## Summary

Build the shared pagination + envelope shape once, with Task as the
reference implementation, so every Phase 4 entity task applies the same
pattern instead of re-deriving it. This is the highest-leverage primitive
in the ADR — nearly every per-entity task depends on it.

## Scope

Implement whatever DEC-001 decided:

- **If cursor**: an opaque `cursor` param every list tool accepts (returned
  from the previous call), internally breaking sort ties on `id`. Build the
  cursor encode/decode helper once (likely in `internal/service` or a
  shared package), not per-entity.
- **If offset**: wire the `Offset` field that already exists at the SQL
  layer (`tasks.go:108`, `collections.go:30-34`, `epics.go:23-28`,
  `sprints.go:26-30`, `projects.go:29-33`) through the service and MCP
  layers — this is "the single cheapest, lowest-risk fix" per the audit,
  pure plumbing for those five.

Either way, land the **response envelope** change:
- `data` becomes `{items, meta: {returned, limit, total_count|has_more,
  next_cursor, hint?}}` — `next_cursor` is `null` when exhausted (cursor
  mode) or `has_more`/`total_count` reflects the true remaining count
  (offset mode).
- `{ok, data}` outer envelope stays unchanged.

Also fix independently-noted issues in the same code path while you're
there (small, same-file, same-concern):
- `internal/mcpadapter/response.go`'s `cappedJSONResult` applies a *second*,
  silent truncation by response byte size (100KB cap) on top of `limit` —
  keep this, but make sure the `meta` returned still accurately reflects
  what actually made it into the response after byte-capping, not what was
  requested.
- `torque_session_list`'s `meta.limit` is hardcoded to
  `defaultGenericListLimit` regardless of the caller-supplied limit
  actually honored (`session_tools.go:193`) — **note**: Session is out of
  ADR-0004's scope, so do not fix this here; flagged only so the pattern
  isn't accidentally copied. Use `model_tools.go:67,81`'s clamp-once/reuse
  pattern as the correct reference instead.

Apply the primitive to **Task's `list`/`get`** as the reference
implementation (Task is "closest to done already" per ADR §5) — this proves
the pattern works end-to-end before the other 6 entities adopt it in
Phase 4.

## Acceptance criteria

- [ ] Shared pagination helper exists and is entity-agnostic (not
      hand-copied per file).
- [ ] `torque_task_list` returns the new envelope shape, verified against a
      dataset large enough to need a second page.
- [ ] Paging through Task results with the new mechanism doesn't
      duplicate/skip rows even when a row is inserted between calls (if
      cursor was chosen — this is the property offset can't guarantee).
- [ ] `meta.total_count` (or `has_more`) is accurate, not a copy of `limit`.
- [ ] The helper is documented well enough (short doc comment, not a design
      doc) that Phase 4 entity tasks can adopt it by following the Task
      example.

## Out of scope

- Rolling the primitive out to the other 6 entities — that's each entity's
  own Phase 4 task.
- Sorting (PRIM-002, separate primitive, though cursor mode's tiebreak
  design should anticipate sort_by being added).
