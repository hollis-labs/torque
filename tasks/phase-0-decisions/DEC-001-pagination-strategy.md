# DEC-001 — Pagination strategy: cursor vs offset

**Phase:** 0 — Decisions
**Status:** done
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

**PROPOSED — pending user sign-off**

### DECIDED: Option 1 — Cursor/keyset pagination now

The subagent that drafted this decision proposed a hybrid (offset now,
cursor deferred until PRIM-002 lands). The project owner reviewed and
overrode that: go straight to cursor/keyset pagination, matching
ADR-0004's stated target directly rather than staging through an interim
offset mode and a later migration. Applies uniformly across all 7 list
tools in ADR-0004's scope (Task, Comment, Project, Epic, Sprint, Issue,
Plan).

### Binding spec for PRIM-001 (cursor mode)

Promoted from the subagent's non-binding sketch — this is now what
PRIM-001 implements, not a future-migration placeholder:

- **Cursor encoding:** `base64url(JSON{v:1, sb:"<sort_by field>",
  sd:"asc"|"desc", sv:"<string-encoded last sort value>", id:"<last row
  id>"})` — opaque to the caller, returned verbatim from the previous
  call's `meta.next_cursor`.
- **Validation:** server decodes and checks `sb`/`sd` match the request's
  current `sort_by`/`sort_dir` — reject with `arg_invalid` on mismatch;
  cursors aren't portable across different sort orders.
- **WHERE-clause pattern:** for `asc`, `(sort_col, id) > (sv, id)` (tuple
  comparison, or the two-clause SQL equivalent
  `sort_col > sv OR (sort_col = sv AND id > id)`); flip the inequality for
  `desc`. Every list tool tiebreaks on `id` ascending regardless of
  primary sort direction — one consistent secondary order, not
  per-entity bikeshedding.
- **`next_cursor`:** `null` when the page's row count is less than
  `limit` (exhausted); otherwise built from the last row actually
  included in the response, computed *after* `cappedJSONResult`'s
  byte-size truncation — if the byte cap drops rows below what the
  store returned, the cursor must reflect what actually shipped, not
  what was fetched.
- **Response `meta` shape:** `{items, meta: {returned, limit,
  total_count|has_more, next_cursor}}` per the ADR §3 envelope text,
  literally — no offset-mode field-omission question to resolve since
  offset mode isn't being built.
- **`total_count` vs `has_more`:** still open for PRIM-001 to pick
  (ADR allows either) — cursor mode doesn't force one over the other;
  `has_more` is cheaper (fetch `limit+1`, trim) and is the recommended
  default absent a concrete need for exact counts.
- **Scope:** all 7 in-scope list tools get cursor pagination directly;
  there's no "five entities with existing Offset plumbing get it first"
  staging — the dead `Offset` fields in `TaskFilter`/`CollectionFilter`/
  `EpicFilter`/`SprintFilter`/`ProjectFilter` are superseded by the
  cursor mechanism and can be left alone or removed as dead code at
  PRIM-001's discretion (Collection itself is out of ADR-0004's scope
  regardless).

### Not applicable (superseded by the override)

The subagent's offset-mode spec and its three ambiguities (migration
compatibility, `next_cursor` presence in offset mode, revisit trigger)
no longer apply — there's no offset mode and no future migration to plan
for. Left out of this file rather than kept as dead alternatives; see
git history on this file if the original hybrid proposal is ever needed
for reference.
