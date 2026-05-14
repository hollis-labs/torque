# Plans v1

Plans are a coordination task type (`kind=plan`) that group a set of phase-scoped
execution children under a single parent task. A plan never dispatches — the
scheduler's picker skips `kind=plan` rows alongside `kind=parent`. Phases are
stored inline on the plan task's metadata; child tasks link back via
`parent_id` plus a `metadata.phase_id` tag.

## Data model

```
tasks
  id        = CW-...
  kind      = plan
  manual    = true            -- plans never auto-dispatch
  metadata  = {"plan": {"version": 1, "phases": [...]}}

  +-- child tasks (kind=agent|external|...):
        parent_id = <plan-id>
        metadata  = {"phase_id": "ph-1", ...}
```

A plan's `metadata.plan.phases[]` is an ordered array of `{id, name, order, acceptance?}`.
Phase IDs are short slugs (`ph-1`, `ph-2`, ...) generated at add time and never
reused. Order is monotonic; the PlanService keeps it in sync on add.

## Service surface

`service.PlanService` wraps the ordinary `TaskService` with the following
conveniences:

- `Create(input)` — builds a `kind=plan` task, prefills `metadata.plan`.
- `Get(planID)` — decodes the plan + returns a child roll-up.
- `AddPhase(planID, name, acceptance)` — appends a phase, returns the new id.
- `RemovePhase(planID, phaseID)` — removes a phase. Rejected if any child
  still references `phaseID` via `metadata.phase_id`.
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

## Non-goals in v1

- No auto-inject of plan context into child task prompts (deferred to v2).
- No drag-reorder of tasks across phases in the GUI (v2).
- No plan-level boundary rules / lifecycle hooks (v2).
- No plan templates (v2).
