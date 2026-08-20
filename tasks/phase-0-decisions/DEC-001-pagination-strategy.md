# DEC-001 — Pagination strategy: cursor vs offset

**Phase:** 0 — Decisions
**Status:** in-progress
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

### Recommendation: Option 3 — Hybrid (offset + `has_more` now, cursor deferred behind an explicit trigger)

**Rationale.** Ship offset pagination now because it is genuinely the
cheapest fix available: five stores (`tasks`, `collections`, `epics`,
`sprints`, `projects`) already have working `Limit`/`Offset` at the SQL
layer, so PRIM-001 is pure plumbing through service and MCP, not new
capability. The correctness hazard cursor exists to solve — page
boundaries shifting as concurrent writes change row order, made worse once
sort is selectable — isn't live yet: zero `OrderBy`/`sort_by` fields exist
anywhere in the store layer today (PRIM-002 is a separate, unscheduled
primitive), so there is no changing sort key for offset to interact badly
with, and agent-driven list calls are typically single-shot reads rather
than deep pagination through actively-mutating result sets. Building
cursor's opaque-token encode/decode machinery now means designing a
sort-key tiebreak scheme against columns that aren't sortable yet, then
likely re-deriving it once PRIM-002 actually lands — better to pay that
cost when sort is real, not speculatively. This is not an open-ended
"revisit someday": **the arrival of PRIM-002 (real `sort_by`/`sort_dir` per
entity) is the trigger to re-open this decision**, since that's exactly
when offset-pagination-plus-changing-sort-order stops being theoretical and
becomes a real bug.

### Concrete spec for PRIM-001 (offset mode)

- **Metric: `has_more`, not `total_count`.** Fetch `limit + 1` rows from
  the store, trim to `limit` before serializing, and set
  `has_more = true` if the extra row was present. Cheaper than a second
  `COUNT(*)` query, and `has_more` is the actual question agents ask
  ("should I page further"), not the exact remaining count.
- **Request param:** `offset` (int, default `0`), alongside the existing
  `limit` param — matches the field name already present in every
  affected store's `Filter` struct, no new vocabulary to introduce.
- **Response `meta` shape for offset mode:**
  `{returned, limit, offset, has_more}`. Do **not** include a `next_cursor`
  key in offset mode — emitting it (even as always-`null`) would imply a
  capability offset mode doesn't have. See ambiguity #2 below; this is the
  one place I'm deviating from the ADR §3 envelope text's literal field
  list and flagging it rather than silently picking.
- **Byte-cap interaction (per PRIM-001 scope note on
  `cappedJSONResult`):** compute `has_more` from the store-level
  `limit+1` fetch *before* any byte-size truncation. If byte-capping then
  drops rows below what `has_more` already indicated, `returned` must
  reflect the actual post-byte-cap count that shipped in the response, and
  `has_more` stays `true` (there's more either way — from the store, the
  byte cap, or both). `returned` must never be a copy of `limit`.
- **Next offset for the caller:** `offset + returned` — plain arithmetic,
  no helper needed; this is intentionally simpler than a cursor because
  offset mode doesn't need to hide anything from the caller.
- **Scope:** apply to the five entities with existing `Offset` plumbing
  (Task, Collection, Epic, Sprint, Project) per the audit's table; the two
  list tools without a store-level `Offset` field at all (per the audit's
  "Issue, Plan, Template, Artifact, Comment, Checkpoint, Subtodo, Run,
  Session" row) are unaffected by this decision and follow their own
  per-entity Phase 4 tasks.

### Deferred cursor design (non-binding — for whoever re-opens this when PRIM-002 lands)

Sketched now so the eventual migration doesn't re-derive it from scratch,
but explicitly **not** what PRIM-001 implements:

- Cursor = `base64url(JSON{v:1, sb:"<sort_by field>", sd:"asc"|"desc",
  sv:"<string-encoded last sort value>", id:"<last row id>"})`.
- Server decodes and validates `sb`/`sd` match the request's current
  `sort_by`/`sort_dir` — reject with `arg_invalid` on mismatch; cursors
  aren't portable across different sort orders.
- WHERE-clause pattern: for `asc`, `(sort_col, id) > (sv, id)` (tuple
  comparison, or the two-clause SQL equivalent
  `sort_col > sv OR (sort_col = sv AND id > id)`); flip the inequality for
  `desc`. All list tools tiebreak on `id` ascending regardless of primary
  sort direction — one consistent secondary order, not per-entity
  bikeshedding.
- `next_cursor` is `null` when the page's row count is less than `limit`
  (exhausted); otherwise built from the last row actually included in the
  response (post-byte-cap, same principle as `has_more` above).

### Ambiguities for the project owner to weigh in on

1. **Migration compatibility.** When PRIM-002 triggers the cursor
   revisit, should the tool schema keep `offset` working alongside the new
   `cursor` param (deprecate, don't break), or is a breaking schema change
   to MCP list tools acceptable at that point? Not decided here.
2. **`next_cursor` field presence in offset mode.** I chose to omit it
   entirely from `meta` rather than include it as always-`null`, since the
   ADR §3 envelope text (`{items, meta: {returned, limit,
   total_count|has_more, next_cursor, hint?}}`) reads as if `next_cursor`
   is a permanent field in the shared shape across both modes. If a
   stable, mode-independent `meta` schema is valued over strict honesty
   about what offset mode can't do, flip this.
3. **Trigger condition.** I've tied the cursor revisit to PRIM-002 landing
   specifically. If there's a reason to revisit sooner (e.g. a concrete
   report of duplicate/skipped rows in production before sort ships),
   that should override the trigger — flagging since I can't assess actual
   production paging-depth/write-concurrency patterns from here.
