# Defining an auto-run task

A task runs unattended when the scheduler picks it up: `manual=false`, a status
the picker takes (`todo`), and a profile that resolves. Create always forces
`manual=true`, so promotion is a separate, explicit call.

## Minimum

```
torque_task_create  {title, description, project_id}      # manual=true, inert
torque_task_update  {id, working_dir}                     # if the project/template does not set it
torque_task_update  {id, manual: false}                   # promotion: now dispatchable
```

Everything else comes from the first layer that sets it:

| Field | Precedence |
| --- | --- |
| executor, launch_profile / agent_profile, on_done, max_retries, quality_gates | task > template > project `execution_defaults` > global |

Global defaults: executor `cli`, profile `default` (profiles.yaml's `default`
key, else the builtin claude-code profile), on_done `review`, max_retries `3`,
no gates.

A template's `on_done=review` and `max_retries=3` are indistinguishable from
"not set", so a project default replaces them.

## Project defaults

`execution_defaults` is a JSON object on the project (`torque_project_create` /
`torque_project_update`, or `execution_defaults` on the HTTP project routes):

```json
{"agent_profile": "implementer", "on_done": "close", "max_retries": 2, "quality_gates": ["subtodos"]}
```

Keys: `executor`, `launch_profile`, `agent_profile`, `on_done`, `max_retries`,
`quality_gates`. Unknown keys, unknown profiles and unknown gates are rejected.
A task that names either `launch_profile` or `agent_profile` keeps its own pair.

## Profiles

An unknown `agent_profile` / `launch_profile` is rejected at task and template
create/update. A task with neither runs on `default`; `worker.implementer` and
role/tier-only tasks also resolve to `default`.

## Tether executor

`executor: tether` runs the task as a plain Tether session instead of a local
CLI process. It is always registered; it refuses dispatch (permanent block)
unless `TORQUE_TETHER_URL` is set.

Per-task metadata:

| Key | Meaning |
| --- | --- |
| `tether_launch` | Tether launch id. Falls back to `launch_profile`, then `TORQUE_TETHER_LAUNCH`. No launch id anywhere blocks the task. |
| `tether_worker` | Optional Tether worker; falls back to `TORQUE_TETHER_WORKER`. |
| `tether_stop_after_final_turn` | Reaper grace (e.g. `30s`); falls back to `TORQUE_TETHER_STOP_AFTER_FINAL_TURN`. |
| `tether_route` | Optional route object passed through on create. |

Environment (read when `torque serve` / `torque mcp` starts):

| Variable | Default | Meaning |
| --- | --- | --- |
| `TORQUE_TETHER_URL` | unset | `http(s)://...` or `unix:/path`. Required. |
| `TORQUE_TETHER_TOKEN` | unset | Bearer token, if the Tether endpoint needs one. |
| `TORQUE_TETHER_LAUNCH` | unset | Default launch id. |
| `TORQUE_TETHER_WORKER` | unset | Default worker. |
| `TORQUE_TETHER_STOP_AFTER_FINAL_TURN` | unset | Default reaper grace. |
| `TORQUE_TETHER_DEFER_MAX` | `30m` | How long a capacity-deferred launch waits before the task is blocked (not failed). |
| `TORQUE_TETHER_RUN_TIMEOUT` | `2h` | Run ceiling after launch. |
| `TORQUE_TETHER_POLL_INTERVAL` | `3s` | Session/event poll interval. |

### Concurrency

Torque admits one run per Torque project at a time (the picker's per-project
slot). Tether's own execution caps therefore only defer launches when runs from
different Torque projects overlap; within a project Torque serialises first. A
capacity refusal leaves the Tether session `created` and the executor retries
the launch every 15s until `TORQUE_TETHER_DEFER_MAX`, then blocks the task.

### Launch ids verified live

On live Tether (verified 2026-10-31 on scratch tasks CW-20261031-0005 and
CW-20261031-0006) the `taskrun-scratch`, `taskrun-implementer` and
`taskrun-reviewer` launches work end to end. `general-worker` also works.

### Create-time failures

- 4xx from Tether on create (unknown launch, bad body, key conflict) is a
  permanent block: retrying cannot help.
- A 5xx such as `502 worker_unavailable` is treated as a transport error, not a
  permanent block: the executor retries create up to 3 times in-process (2s
  apart; the idempotency key makes this safe), then returns the error so the
  scheduler's normal retry policy (`max_retries`) applies. This is acceptable
  for a transient worker outage, but a worker that stays down consumes retries
  and ends as a failed task rather than a blocked one.

### Known live-Tether limits

Most launches for projects other than the scratch/taskrun ones currently fail
on live Tether with `project_layer_unprotectable` (design-lab: `repo_root`
missing) or `project_root_missing` (go-providers, hadron). These come from the
Tether / Agent Ops project catalog, not from Torque, and are not fixable here;
they are routed to Agent Ops. Until the catalog entries are repaired, use
`taskrun-*` / `general-worker` launches or the `cli` executor for those
projects.
