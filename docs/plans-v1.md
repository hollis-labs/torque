# Plans v1

Plans are a coordination task type (`kind=plan`) that group a set of phase-scoped
execution children under a single parent task. A plan never dispatches — the
scheduler's picker skips `kind=plan` rows alongside `kind=parent`. Phases are
records in `plan_phases` (migration 043); child tasks link back via `parent_id`
plus the `tasks.phase_id` column.

## Data model

```
tasks
  id        = CW-...
  kind      = plan
  manual    = true            -- plans never auto-dispatch

plan_phases                   -- one row per (plan_id, phase_id)
  name, display_order, acceptance
  state                = pending | started | completed
  lifecycle_generation = 1, bumped when a completed phase is reopened
  started_at, completed_at, revision
  deleted_at                  -- tombstone; the row is kept

  +-- child tasks (kind=agent|external|...):
        parent_id = <plan-id>
        phase_id  = "ph-1"    -- must name a live phase of parent_id
```

Phase IDs are short slugs (`ph-1`, `ph-2`, ...) generated at add time and never
reused: a removed phase is tombstoned, and new ids are allocated above every
suffix the plan has issued. `tasks.phase_id` is held to the parent plan's live
phases by database triggers, so every write path is covered.

Existing callers keep working. A child created or updated under a plan with
`metadata.phase_id` has that value recorded in `phase_id` (rejected if the plan
has no such phase), and `Get` still returns `{id, name, order, acceptance?}`
phases at `plan.phases` and `task.metadata.plan.phases`, now built from the
records with `state` and `lifecycle_generation` added. Nothing writes
`metadata.plan.phases` any more. Migration 043 backfilled phases and memberships
from the old metadata; a child naming a phase its plan does not have was not
given one and is listed in `plan_phase_repairs`.

## Service surface

`service.PlanService` wraps the ordinary `TaskService` with the following
conveniences:

- `Create(input)` — builds a `kind=plan` task and its phase records.
- `Get(planID)` — decodes the plan + returns a child roll-up.
- `AddPhase(planID, name, acceptance)` — appends a phase, returns the new id.
- `RemovePhase(planID, phaseID)` — removes (tombstones) a phase. Rejected if any
  child still belongs to `phaseID`.
- `ListChildren(planID, phaseID?)` — lists children of the plan, optionally
  narrowed by phase.
- `Progress(planID)` — totals + per-phase done/blocked counts.

## HTTP surface

All endpoints live under `/api/v1/plans`:

- `GET /plans` — list tasks where `kind=plan`.
- `POST /plans` — create a plan with optional initial phases.
- `GET /plans/{id}` — plan task + decoded metadata + progress roll-up.
- `POST /plans/{id}/phases` — append a phase.
- `DELETE /plans/{id}/phases/{phase_id}` — remove a phase.
- `GET /plans/{id}/children?phase_id=...` — list plan children, optionally
  filtered by phase.

## MCP surface

Plan tools live alongside `torque_task_*`:

- `torque_plan_create(title, description?, phases?, ...)`
- `torque_plan_get(plan_id)`
- `torque_plan_add_phase(plan_id, name, acceptance?)`
- `torque_plan_remove_phase(plan_id, phase_id)`
- `torque_plan_list_children(plan_id, phase_id?)`

## Creating a child task under a plan

Use the existing `torque_task_create` or `POST /tasks` with:

```json
{
  "title": "Ship migration 014",
  "parent_id": "CW-20260417-0400",
  "metadata": {"phase_id": "ph-1"}
}
```

The plan GUI's "Add task to phase" action pre-fills both fields.

## Branch & PR strategy

A plan must also pick how its children integrate with `main` — per-task
branches vs. one shared branch, and continuous merges vs. PRs deferred to
program end. [plan-branch-strategies.md](plan-branch-strategies.md) is the
canonical reference. In particular, a plan that uses **option-4** (a shared
long-lived branch with PRs opened at program end) MUST include a terminal
reconcile-and-build task as its final child — a clean textual merge is not a
working merge.

## Non-goals in v1

- No auto-inject of plan context into child task prompts (deferred to v2).
- No drag-reorder of tasks across phases in the GUI (v2).
- No plan-level boundary rules / lifecycle hooks (v2).
- No plan templates (v2).
