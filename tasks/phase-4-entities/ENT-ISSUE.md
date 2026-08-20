# ENT-ISSUE — Issue: merge list+search, DB-level limit, status filter, bulk ops

**Phase:** 4 — Per-entity rollout
**Status:** done
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

- [x] `torque_issue_list` and `torque_issue_search` are merged into one
      tool (old dual-tool redundancy removed) — confirm this is an
      acceptable breaking change per ADR-0004's Consequences section (it
      is: "no compat guarantee has been made to external callers yet").
- [x] The merged tool pushes `limit` to the DB layer (no more full-fetch-
      then-truncate).
- [x] Pagination (PRIM-001), sort (PRIM-002), and a `status` filter all
      work on the merged tool.
- [x] `bulk_update` exists per PRIM-003.
- [x] `bulk_transition` works for issues via the shared Task FSM path.

## Out of scope

- Any change to `TaskService.BulkTransition` itself — reused as-is.

## Execution notes

**Worktree was stale at start.** This worktree branched from `main` before
PRIM-001/PRIM-002/PRIM-003 (and `tasks/`, ADR-0004 itself) had landed —
none of the primitives existed on disk yet. Fast-forward merged local
`main` (b022082) into the branch first to pick them up; no conflicts, no
worktree-local commits existed yet.

**Merge shape.** `torque_issue_search` is removed; `torque_issue_list`
absorbs its behavior via an optional `query` param (empty = pure filtered
list, non-empty = the old search substring match over id/title/body).
Added `status` filter, `sort_by`/`sort_dir` (reusing Task's
`taskSortAllowList`/defaults since issue rows are literally `TaskRecord`
rows), and cursor pagination (`cappedCursorJSONResult`, same helper Task's
reference implementation uses).

**Service-layer collapse, not just the MCP tool.** The redundancy in
`issue_tools.go` traced back to two separate `IssueService` methods
(`List(projectID)` / `Search(query, projectID, limit)`) that both just
called `s.tasks.List(TaskFilter{...})` with near-identical filters. Merged
these into one `IssueService.List(IssueListInput)` (Query optional, Limit
always passed through) rather than leaving the duplication at the service
layer while only papering over it in the adapter. This method is also used
by `internal/httpserver/issues.go`'s `listIssues`/`searchIssues` handlers
(not part of this ADR's MCP scope, but the only other caller) — updated
both call sites to the new signature and preserved external HTTP behavior
exactly (`listIssues` still unbounded/no query; `searchIssues` still
rejects an empty `q` with 422, now enforced explicitly in the HTTP handler
since that validation moved out of the now-optional-query service method).

**`bulk_update`** follows PRIM-003 exactly: `IssueService.BulkUpdate` is a
thin `RunBulk` wrapper over the existing single-item `Update` (which
already enforces kind=issue scoping via `Get`), and
`torque_issue_bulk_update` shares one `buildIssueUpdateInput` builder with
`torque_issue_update` so single/bulk can't drift on presence-in-payload
semantics — same pattern `buildTaskUpdateInput` established for Task.

**`bulk_transition`** is a straight passthrough to
`a.svc.Task.BulkTransition` (untouched, per Out of scope), wired under
`torque_issue_bulk_transition` with the same `{success, failed, errors}`
shape as `torque_task_bulk_transition`.

**Known limitation (pre-existing, not introduced or fixed here):** a
freshly created issue starts at `status=backlog`, which is not a key in
`validTransitions` (`internal/service/task.go`) — so neither
`torque_task_transition` nor the new `torque_issue_bulk_transition` can
move an issue out of `backlog` without `force=true` on
`torque_task_transition` first. This is documented in the new tool's
description and covered by a test
(`TestFullStack_IssueBulkTransition_DelegatesToTaskFSM`) asserting the
from-backlog case fails cleanly (1 failed, 0 success) rather than silently
succeeding. Fixing the FSM itself is out of scope (ADR-0004 doesn't touch
`TaskService.BulkTransition`/`validTransitions`).

**Test coverage added.** There was no `issue_tools_test.go` before this
change (zero MCP-level coverage for any issue tool). Added
`internal/mcpadapter/issue_tools_test.go` covering: merged list/search
filters (project_id, query, status, kind-scoping away from a same-title
plain task), DB-level-limit + cursor pagination round-trip (limit=1
across 3 issues, visits every id exactly once), `bulk_update` partial
success (not_found vs arg_invalid distinctions), `bulk_transition`
delegation including the backlog-FSM edge case, and a registration check
that `torque_issue_search` is gone while the three new/changed tools
exist. Updated `internal/service/issue_test.go` to call the merged
`List(IssueListInput)` instead of the removed `List(projectID)`/
`Search(query, projectID, limit)` pair.

**Verification:** `go build ./...`, `go vet ./...`, and `go test ./...`
all pass (full suite, not just the touched packages).
