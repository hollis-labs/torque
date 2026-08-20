# ENT-COMMENT — Comment: update/delete, pagination/sort/filter, bulk_add

**Phase:** 4 — Per-entity rollout
**Status:** done
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

- [x] `torque_comment_update`/`torque_comment_delete` exist, service-layer
      backed (not adapter-only logic), author-scoped.
- [x] `torque_comment_list` takes `limit`, pagination (per PRIM-001),
      `sort_by`/`sort_dir` (per PRIM-002), and author + date-range filters.
- [x] `torque_comment_search` gets real pagination/sort/filter, stays a
      separate tool from `list`.
- [x] `bulk_add` exists and posts to multiple entity refs in one call with
      partial-success semantics.
- [x] `entity_type` support for `task`/`project`/`epic`/`sprint` verified
      working end-to-end, not just documented.

## Out of scope

- Folding `search` into `list` — ADR explicitly keeps these distinct for
  Comment (unlike Task/Issue).
- Full container resolution for the multi-entity filter (e.g. resolving
  `sprint_id` to every task in that sprint automatically) — `entity_ids`
  answers "comments across this caller-resolved set of refs" but the caller
  still has to resolve the container to ids themselves (e.g.
  `torque_task_list {"sprint_id":...}` first). A bigger lift than this
  ticket's scope; deferred per the corrective-redo brief.

## Execution notes

**Corrective-redo context.** This ticket was previously attempted in worktree
`worktree-agent-a1810c710a891ef34`, which branched from a stale point in
`main` (before PRIM-001/002/003/004, FK-002/003, and five other Phase 4
entities had merged). That attempt was never merged, but its Comment-specific
business logic was high quality, so this redo read its diff for reference
(`git show worktree-agent-a1810c710a891ef34:<path>` per file — plain `git
diff` against that branch degraded to a stat-only summary in this
environment, so full file reads were used instead) and ported it verbatim
onto the current `main` tip, adjusting only what current `main` actually
provides differently than the stale branch assumed.

This session's own worktree had the *same* staleness problem: it was also
branched at the pre-PRIM-001 commit (`c57775d`, matching the prior attempt's
merge-base exactly) even though the task brief asserted the branch already
had Phase 2 primitives + five entities merged. Verified via `git log
HEAD..main` that local `main` had since advanced 45 commits ahead (PRIM-001
through PRIM-004, FK-002/FK-003, and ENT-PROJECT/SPRINT/PLAN/ISSUE/SUBTODO/
TASK) with zero divergent commits on this worktree's branch — so the whole
history was fast-forwarded (`git merge --ff-only main`) before any Comment
work began, rather than building on the stale base and creating another
"not directly mergeable" branch.

**What was built:**
- `internal/service/comment.go`: `CommentService.Update`/`Delete`
  (author-scoped, `*service.PermissionError` on mismatch), `CommentTarget` +
  `BulkAdd` (creates one comment per target via `RunBulk`, keyed by
  `"entity_type:entity_id"` since there's no pre-existing id to key on),
  `ListFiltered` (alias for `Search` used by `torque_comment_list`'s call
  site), and entity_type validation in `Add` against
  `sqlstore.ValidCommentEntityTypes` (task/project/epic/sprint) — closes the
  "any string silently accepted" gap confirmed still present on current
  `main`.
- `internal/persistence/sqlstore/comments.go`: `GetComment`/`UpdateComment`/
  `DeleteComment`, `ErrCommentNotFound`, `CommentRecord.UpdatedAt`,
  `CommentFilter.EntityIDs` (OR-match within one entity_type),
  `CreatedAfter`/`CreatedBefore` range filters, and PRIM-001/PRIM-002 cursor
  + sort support in `SearchComments` (`commentSortColumn`/`commentCursorArg`,
  mirroring `tasks.go`'s `taskSortColumn`/`taskCursorArg` pattern). Default
  order when no `sort_by` is supplied stays the pre-existing
  `created_at DESC, id DESC` so old callers are unaffected.
- `internal/persistence/sqlstore/migrations/031_comment_updated_at.sql`:
  adds `comments.updated_at` (backfilled to `created_at`). Numbered 031, not
  027 — 027 (epic status default) through 030 (archived_at) were already
  taken by already-merged Phase 2-4 work per the corrective-redo brief.
- `internal/mcpadapter/comment_tools.go`: `torque_comment_update`,
  `torque_comment_delete`, `torque_comment_bulk_add` (new tools);
  `torque_comment_list` gains `limit`, cursor pagination, `sort_by`/
  `sort_dir`, `author`, `created_after`/`created_before`, and `entity_ids`;
  `torque_comment_search` gets the same pagination/sort/filter treatment
  while staying a separate tool from `list` per the ADR's explicit Comment
  exception (list defaults oldest-first/chronological-thread,
  search defaults newest-first/cross-entity-recency — same underlying
  `SearchComments` query, different defaults, per ADR-0004 §3).
- `internal/mcpadapter/errors.go` + `internal/service/errors.go`:
  `*service.PermissionError` (new) mapped to `ErrCodePermission`
  (`error.code=permission`) — previously reserved but unmapped by any
  current error path; first mapped case.
- `internal/mcpadapter/response.go`: `defaultCommentListLimit`/
  `maxCommentListLimit` (50/200) constants for `torque_comment_list`.

**Tests added:** `internal/service/comment_test.go` (entity_type validation,
author-scoped update/delete, bulk_add partial success),
`internal/mcpadapter/comment_tools_test.go` (entity_type support across all
four kinds, list limit/sort validation, cursor pagination round-trip with no
dupes/skips, author + date-range filters, entity_ids multi-filter, search
pagination/sort, update/delete permission checks, bulk_add). Extended the
pre-existing `internal/persistence/sqlstore/comments_test.go` (kept its
original tests, appended `TestAddComment_PopulatesUpdatedAt`,
`TestGetUpdateDeleteComment`, `TestSearchComments_EntityIDsFilter`,
`TestSearchComments_CreatedAtRangeFilter`,
`TestSearchComments_CursorPagination`).

**Unrelated bug fixed to unblock `go build ./...`:**
`internal/mcpadapter/issue_tools.go`'s `handleIssueBulkTransition` called
`e.Error()` on a `service.BulkItemError` loop variable, which has no
`Error()` method (only its `.Err` field does) — a pre-existing compile
break from already-merged ENT-ISSUE/ENT-TASK work on `main`, unrelated to
Comment. Fixed to `e.Err.Error()` (one-line fix) since it blocked the whole
module from building.

**Verification:** `go build ./...` and `go test ./...` both pass clean
across the full module (not just the touched packages).
