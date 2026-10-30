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
