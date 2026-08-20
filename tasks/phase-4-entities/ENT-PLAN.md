# ENT-PLAN — Plan: dedicated list tool, update/delete, list_children default limit

**Phase:** 4 — Per-entity rollout
**Status:** done
**Depends on:** PRIM-001, PRIM-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Plan); audit "Per-Entity Findings → Plan"

## Summary

Plan target inventory: `create`, `get`, `list` (**dedicated tool — today
only reachable via `torque_task_list kind=plan`**), `update`, `delete`,
`add_phase`, `remove_phase`, `list_children` (limit becomes a real default
— today it hardcodes the system maximum). `PlanService` itself is already
"one of the better-composed services in the codebase" (versioned metadata,
observer hook, clean `Get`/`Progress` separation) — adapter handlers are
thin throughout, so this is mostly additive tool-surface work, not a
service-layer rewrite.

## Scope

- **Dedicated `torque_plan_list`**: today discovering plans means
  `torque_task_list kind=plan`. Build a dedicated tool mirroring how Issue
  gets its own list/search pair rather than only being reachable through
  the generic Task surface. Apply PRIM-001/PRIM-002.
- **`torque_plan_update`/`torque_plan_delete`**: don't exist today — a
  plan's own title/description can only be reached via the generic
  `torque_task_update`, with no plan-specific kind-guard the way Issue's
  update has one. Add dedicated tools with a kind guard (reject/error if
  called against a non-plan task id).
- **`list_children` default limit fix**: `torque_plan_list_children`
  hardcodes `maxTaskListLimit` (200) — the system's *maximum*, not a
  sensible default — as the only limit, regardless of caller intent
  (`plan_tools.go:153`). This is "the one list tool in the whole surface
  that hardcodes the ceiling rather than a smaller default" per the audit.
  Fix: make `limit` a real caller-adjustable param with a sane smaller
  default.
- **`add_phase`/`remove_phase`**: confirm current state — ADR's target
  inventory lists these as part of Plan's tool set; verify whether they
  already exist and are complete, or need work. (The audit doesn't flag
  gaps here specifically, suggesting they may already be in reasonable
  shape — verify before assuming work is needed.)

## Acceptance criteria

- [x] `torque_plan_list` exists as a dedicated tool (not just
      `torque_task_list kind=plan`), with pagination (PRIM-001) and sort
      (PRIM-002).
- [x] `torque_plan_update`/`torque_plan_delete` exist, kind-guarded against
      non-plan task ids.
- [x] `torque_plan_list_children`'s `limit` param is caller-adjustable with
      a sensible default well below the 200-row system maximum.
- [x] `add_phase`/`remove_phase` verified working (fixed if found broken,
      left alone if already correct).

## Out of scope

- `torque_plan_start` — bypasses `service.Service` to call
  `internal/planstart` directly, which is execution-adjacent (it
  dispatches). ADR-0004 explicitly defers this "with the rest of
  execution" — do not touch it here. Untouched.

## Execution notes

**Note on worktree staleness:** this worktree branched before PRIM-001/
PRIM-002 (`internal/service/pagination`, `cappedCursorJSONResult`,
`taskListCursorEnvelope`) landed on `main`. Fast-forward merged local `main`
(`b022082`, 33 commits ahead) into the worktree branch before starting —
clean fast-forward, no divergent commits, no conflicts — to pick up the
primitives this task depends on rather than reinventing them.

**`torque_plan_list` (`internal/mcpadapter/plan_tools.go`):**
- New tool + `handlePlanList`, built to mirror `handleTaskList`
  (`task_tools.go`) as closely as possible: same sort_by/sort_dir
  allow-list validation via `pagination.ValidateSortBy`/`ValidateSortDir`,
  same cursor decode/validate via `pagination.Decode`/`Cursor.Validate`,
  same `limit+1` over-fetch to compute `has_more` without a `COUNT(*)`.
  `planSortAllowList`/`planSortDefaultBy`/`planSortDefaultDir` are Plan's
  own copies of Task's equivalents (`priority|status|updated_at|created_at`,
  default `priority asc`) — kept separate rather than shared so Plan's list
  surface doesn't silently drift if Task's allow-list changes later.
- Reuses `taskListCursorEnvelope` (task_tools.go) directly for the response
  — plans are Task rows, so no plan-specific envelope variant was needed;
  same pattern `handlePlanListChildren`'s existing "reuse the task envelope"
  comment already established.
- Filter surface: `status`, `priority`, `project_id`, `sprint_id`,
  `epic_id`, `tags`, `search`, plus `limit`/`verbose`/`sort_by`/`sort_dir`/
  `cursor`. Default limit is `defaultGenericListLimit` (100), max
  `maxTaskListLimit` (200) — same convention used for the `list_children`
  fix below, for consistency across Plan's two list tools.
- New `PlanService.List(filter sqlstore.TaskFilter)` (plan.go): force-sets
  `filter.Kind = "plan"` so callers can't widen the result set, then
  delegates straight to `TaskService.List` — a one-line body, matching the
  "additive, not a service-layer rewrite" framing. `TaskService.List`
  already accepts `sqlstore.TaskFilter` directly as its exported signature
  (same as `IssueService`'s internal filter-building), so no new filter
  type was needed.

**`torque_plan_update`/`torque_plan_delete` (new):**
- `PlanService.Update`/`Delete` (plan.go) both route through a new
  `requirePlan(id)` helper (fetch + kind check) before touching anything —
  the kind guard the audit flagged as missing. `requirePlan` is a fresh
  helper used only by the two new methods; the existing inline
  `task.Kind != "plan"` checks in `Get`/`AddPhase`/`RemovePhase` were left
  alone rather than refactored onto it (out of scope — no reason to touch
  working code for this task).
- `PlanUpdateInput` (title/description/priority/project_id/sprint_id/
  epic_id/tags, all `*T` = presence-based) mirrors `PlanCreateInput` minus
  `Phases` (structural — edited via add/remove_phase, not Update) plus
  `Tags` (settable on create, so settable on update too, matching Task's
  tag-replace convention). Empty-string project/sprint/epic clears the
  association (`sql.NullString{Valid: v != ""}`), matching
  `torque_task_update`'s existing semantics.
- `Delete` reuses `TaskService.Delete` as-is (runs/artifacts/comments
  cascade already generic); child tasks are NOT cascaded — `parent_id`'s
  `ON DELETE SET NULL` FK (migration 013/028) orphans them cleanly, no
  extra code needed.
- MCP handlers (`handlePlanUpdate`/`handlePlanDelete`) are thin: build the
  input from presence-checked args (`reqHasArg`, not zero-value checks, so
  this doesn't inherit Task's own known `priority` zero-value quirk — that
  fix belongs to ENT-TASK, not replicated here since this is a new
  handler), call the service method, map errors via `errFromService`
  (kind-guard `ValidationError` → `arg_invalid`, field=`kind`, matching
  Issue's update pattern).

**`torque_plan_list_children` limit fix (`plan_tools.go`):**
- Two bugs, not one: (1) `limit` was hardcoded to `maxTaskListLimit` (200,
  the system maximum) with no caller override — the fix scope's headline
  item; (2) more subtly, that hardcoded limit was never actually *applied*
  — `tasksToEnvelope`/`cappedJSONResult` report `meta.limit` but don't
  slice `items` down to it themselves (confirmed against
  `handleIssueList`, which explicitly does `if len(issues) > limit {
  issues = issues[:limit] }` before calling the same envelope helper — the
  established truncate-before-envelope pattern). So every
  `list_children` call was silently returning the FULL unbounded child set
  with `meta.limit` just mislabeled at 200, not actually capping anything.
- Fixed both: added a real `limit` param (default `defaultGenericListLimit`
  = 100, max `maxTaskListLimit` = 200, via `clampLimit` — same helper/
  defaults every other generic list tool in the surface uses), and added
  the missing `if len(children) > limit { children = children[:limit] }`
  truncation before the envelope call.

**`add_phase`/`remove_phase` — verified, one real bug found and fixed:**
- `add_phase` (`PlanService.AddPhase`): verified correct as-is. No changes.
- `remove_phase`: found a genuine contract break while verifying, not
  speculative — `torque_plan_remove_phase`'s own docstring promises
  "Rejected with error.code=conflict if any child task still references
  the phase", but `PlanService.RemovePhase` was returning
  `*service.ValidationError` for that exact case, which
  `mcpadapter.mapServiceError` maps to `arg_invalid`, not `conflict`.
  Fixed by changing that one return to `*service.ConflictError`
  (`plan.go`), which `mapServiceError` correctly maps to `conflict`.
  - This is a service-layer error-type change with two callers, so both
    needed updating: `internal/service/plan_test.go`'s
    `TestPlanRemovePhase_BlocksWhenChildrenReferencePhase` now asserts
    `*service.ConflictError` instead of `*service.ValidationError`; and
    `internal/httpserver/plans.go`'s `removePlanPhase` HTTP handler — which
    only special-cased `*service.ValidationError` → 422, so without a fix
    it would have silently regressed to a generic 500 for this case — now
    checks `*service.ConflictError` → 409 first, mirroring the existing
    `writeTemplateError`/`writeCheckpointError` "ConflictError before
    ValidationError" pattern already established elsewhere in
    `internal/httpserver`. New MCP-level regression test:
    `TestFullStack_PlanRemovePhase_BlockedByChildren_IsConflict`.
  - The MCP adapter handler itself (`handlePlanRemovePhase`) needed no
    change — it already just calls `errFromService(err)` generically, so
    it picked up the corrected `conflict` code automatically once the
    service-layer error type changed.

**Tests added** (`internal/mcpadapter/plan_tools_test.go`,
`internal/service/plan_test.go`): kind-scoping and cursor
pagination/sort for `torque_plan_list` (mirroring `task_tools_test.go`'s
`TestFullStack_TaskList_*` cursor acceptance tests); update/delete happy
path plus kind-guard rejection for both; `list_children`'s adjustable-limit
and corrected-default behavior; the `remove_phase` conflict-code regression
test above; the `RemovePhase` service-level `ConflictError` assertion
update.

**Verification:** `go build ./...`, `go vet ./...`, and `go test ./...`
(full repo, all packages) all pass with no other changes needed.
