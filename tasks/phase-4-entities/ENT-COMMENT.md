# ENT-COMMENT — Comment: update/delete, pagination/sort/filter, bulk_add

**Phase:** 4 — Per-entity rollout
**Status:** todo
**Depends on:** PRIM-001, PRIM-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §3 (Search folds into list — Comment exception), §5
(Comment); audit "Per-Entity Findings → Comment"

## Summary

Comment target inventory: `add`, `list` (per-entity thread, now paginated/
sortable/filterable by author+date range), `search` (cross-entity,
recency-ordered — stays a genuinely distinct tool, not merged with `list`),
`update`, `delete` (both new — no correction path exists today), `bulk_add`
(same note to many entity refs at once).

## Scope

- **`update`/`delete`**: no `Update`/`Delete` method exists at any layer
  today (`service/comment.go`) — this is new capability, not a wiring fix.
  "Comments never drive lifecycle" stays true; this just makes them
  editable/removable by their author (confirm author-only enforcement, not
  open to any caller).
- **`list`**: apply PRIM-001/PRIM-002 (pagination + sort). Currently
  unbounded before a hardcoded 100-item truncation
  (`comment_tools.go:68-92`) with no `limit` param at all — fix that as
  part of adopting the primitive. Add author + date-range filtering
  (`CommentRecord.CreatedAt` already exists, just unfiltered).
- **`entity_type` closes a documented gap**: it genuinely supports `task`,
  `project`, `epic`, `sprint` today (Issue/Plan already covered via
  `entity_type=task` since they're Task rows) but per the ADR this has been
  "documented as future, never shipped" — verify current state and close
  whatever's actually missing.
- **`search`**: apply real pagination/sort/filter (currently hardcoded,
  unbounded-then-truncated per ADR §3) — keep it a distinct tool from
  `list`, per the ADR's explicit exception (different query shape:
  cross-entity, recency-ordered vs. per-entity chronological thread).
- **`bulk_add`**: post the same comment text to many entity refs at once
  (e.g. broadcast a note to every task in a sprint) — audit notes this as a
  low-priority gap but it's in ADR-0004's target inventory.
- **Multi-entity `EntityID`**: audit notes `EntityID` is single-valued only
  today ("comments across every task in sprint X" isn't answerable) — this
  overlaps with the `list` filter work above; confirm scope covers it or
  explicitly defer with a note if it's a bigger lift than expected.

## Acceptance criteria

- [ ] `torque_comment_update`/`torque_comment_delete` exist, service-layer
      backed (not adapter-only logic), author-scoped.
- [ ] `torque_comment_list` takes `limit`, pagination (per PRIM-001),
      `sort_by`/`sort_dir` (per PRIM-002), and author + date-range filters.
- [ ] `torque_comment_search` gets real pagination/sort/filter, stays a
      separate tool from `list`.
- [ ] `bulk_add` exists and posts to multiple entity refs in one call with
      partial-success semantics.
- [ ] `entity_type` support for `task`/`project`/`epic`/`sprint` verified
      working end-to-end, not just documented.

## Out of scope

- Folding `search` into `list` — ADR explicitly keeps these distinct for
  Comment (unlike Task/Issue).
