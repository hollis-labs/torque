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
`metadata.plan.phases` any more: on a raw task read (`task_get`, `task_list`, `plan_list`) it is a frozen legacy copy, left in place so a code revert still has the old data; only the plan endpoints overlay the records. Migration 043 backfilled phases and memberships
from the old metadata; a child naming a phase its plan does not have was not
given one and is listed in `plan_phase_repairs`.

## Phase lifecycle

A phase is `pending`, `started` or `completed`. It is derived from its members
and re-evaluated inside the transaction of anything that changes them: a member's
status change, a member added to or moved out of the phase, a member deleted, or
a member created already done.

- A pending phase **starts** once any member is `doing`, `review`, `blocked`,
  `paused` or `done`. It appends `phase.started`.
- A phase **completes** when it has members and all of them are `done` (review,
  archived and the rest are not success). It appends `phase.completed`. If one
  change both starts and completes it (the last member going straight to `done`,
  or a member created `done`), both events are written, started first, each with
  its own id and the same cause.
- A blocked member keeps the phase started; there is no `phase.blocked`.
- A completed phase never reverts on its own. `ReopenPhase` returns it to
  `started`, bumps `lifecycle_generation` and appends `phase.started` with the
  new generation and `reopened: true`. Do it before adding or reactivating work.
- A phase left with no members keeps its state; an empty phase has nothing to
  move it and needs an explicit start or complete (not yet exposed).
- Postgres: the phase row is read `FOR UPDATE` before members are counted.

Each event is the phase's own (`msg://phase/torque/<plan>/<phase>`; phase ids are
per plan and the subject never changes), carries `progress{done,total}` and
`lifecycle_generation`, and, when a member's `task.state_changed` caused it,
names that event as its `corr.causation_id`. A membership change has no task
event and so no causation. `scope.plan_id` and `scope.phase_id` on task events
come from these records.

## Service surface

`service.PlanService` wraps the ordinary `TaskService` with the following
conveniences:

- `Create(input)` — builds a `kind=plan` task and its phase records.
- `Get(planID)` — decodes the plan + returns a child roll-up.
- `AddPhase(planID, name, acceptance)` — appends a phase, returns the new id.
- `RemovePhase(planID, phaseID)` — removes (tombstones) a phase. Rejected if any
  child still belongs to `phaseID`.
- `ReopenPhase(planID, phaseID)` — reopens a completed phase (generation + 1).
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
