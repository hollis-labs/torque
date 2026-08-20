# ENT-PLAN — Plan: dedicated list tool, update/delete, list_children default limit

**Phase:** 4 — Per-entity rollout
**Status:** todo
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

- [ ] `torque_plan_list` exists as a dedicated tool (not just
      `torque_task_list kind=plan`), with pagination (PRIM-001) and sort
      (PRIM-002).
- [ ] `torque_plan_update`/`torque_plan_delete` exist, kind-guarded against
      non-plan task ids.
- [ ] `torque_plan_list_children`'s `limit` param is caller-adjustable with
      a sensible default well below the 200-row system maximum.
- [ ] `add_phase`/`remove_phase` verified working (fixed if found broken,
      left alone if already correct).

## Out of scope

- `torque_plan_start` — bypasses `service.Service` to call
  `internal/planstart` directly, which is execution-adjacent (it
  dispatches). ADR-0004 explicitly defers this "with the rest of
  execution" — do not touch it here.
