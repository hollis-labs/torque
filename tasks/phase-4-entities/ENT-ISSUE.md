# ENT-ISSUE — Issue: merge list+search, DB-level limit, status filter, bulk ops

**Phase:** 4 — Per-entity rollout
**Status:** todo
**Depends on:** PRIM-001, PRIM-002, PRIM-003
**Blocks:** SWEEP-001
**Source:** ADR-0004 §3 (Search folds into list), §5 (Issue); audit
"Per-Entity Findings → Issue"

## Summary

Issue target inventory: `create`, `get`, `list` (merged with `search` —
same redundancy pattern as Task), `update`, `delete`, `bulk_update`,
`bulk_transition` (shares Task's FSM since issues are Task rows). Service/
adapter alignment is already clean here — `IssueService` correctly wraps
`TaskService`, adapter is thin. This is one of the lighter-lift entity
tasks.

## Scope

- **Merge `list`/`search`**: today `torque_issue_list` does a full
  unbounded DB fetch then truncates in Go (`IssueService.List` never passes
  `Limit`), while `torque_issue_search` correctly pushes `limit` to the DB
  layer (`issue_tools.go:126-135` vs `138-146`). Merge into one tool
  following the `list`'s query but fixed to push `limit` to the DB like
  `search` already does — matching the same fix pattern ADR-0004 applies to
  Task.
- **Apply PRIM-001/PRIM-002** to the merged tool.
- **Status filter**: no status filter exists on issue_list today (issues
  start at `backlog` but can transition via the generic task surface) — add
  it.
- **`bulk_update`**: apply PRIM-003's pattern.
- **`bulk_transition`**: Issue shares Task's FSM — reuse
  `TaskService.BulkTransition` directly (already built for Task, don't
  reimplement), just add the MCP tool wiring for Issue's tool namespace if
  it doesn't already delegate there.

## Acceptance criteria

- [ ] `torque_issue_list` and `torque_issue_search` are merged into one
      tool (old dual-tool redundancy removed) — confirm this is an
      acceptable breaking change per ADR-0004's Consequences section (it
      is: "no compat guarantee has been made to external callers yet").
- [ ] The merged tool pushes `limit` to the DB layer (no more full-fetch-
      then-truncate).
- [ ] Pagination (PRIM-001), sort (PRIM-002), and a `status` filter all
      work on the merged tool.
- [ ] `bulk_update` exists per PRIM-003.
- [ ] `bulk_transition` works for issues via the shared Task FSM path.

## Out of scope

- Any change to `TaskService.BulkTransition` itself — reused as-is.
