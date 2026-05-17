# Getting Started

## Prerequisites

- Go 1.26.x
- Node/npm for the GUI workflow if you are editing frontend code

## Main Commands

```bash
torque serve
torque mcp
torque version
```

## Local Defaults

Without extra configuration:

- SQLite is used
- The DB path defaults to `torque.db`
- The HTTP server listens on port `8990`
- Runtime data goes under `.torque/`

## Useful Environment Variables

```bash
export TORQUE_DB_PATH=/path/to/torque.db
export TORQUE_HTTP_PORT=8990
export TORQUE_DATA_DIR=.torque
export TORQUE_PROFILES_PATH=/path/to/profiles.yaml
```

To use Postgres instead of SQLite:

```bash
export TORQUE_POSTGRES_DSN=postgres://...
```

## Notes About `serve` vs `mcp`

`torque serve` starts:

- the HTTP API
- the scheduler
- the GUI

`torque mcp` starts:

- the MCP server over stdio

The stdio MCP process does not create or own a live scheduler instance. `torque_scheduler_status` still works there by proxying read-only state from the local `torque serve` process at `http://127.0.0.1:$TORQUE_HTTP_PORT/api/v1/scheduler/status`, so an MCP client can tell whether auto-dispatch is live. `torque_scheduler_toggle` remains serve-only because stdio MCP does not own the scheduler.

## Sprint Kickoff In `approve_sprint` Mode

`approval_mode=approve_sprint` is a completion gate, not a "start sprint" action. `torque_sprint_approve` only moves sprint tasks that are already in `review` to `done`, so it will correctly return `0 tasks approved` before any sprint task has run.

To start a sprint in that mode:

1. Create or identify the sprint tasks.
2. Promote the first task or batch of tasks to `manual=false` with `torque_task_update`.
3. Confirm the scheduler is enabled with `torque_scheduler_status`.
4. Let the scheduler dispatch those tasks.
5. Later, when tasks reach `review`, use `torque_sprint_approve` to approve them to `done`.

## Task Creation Safety Override

`torque_task_create` currently forces every new task to `manual=true` per CW-20260417-0133, even if the caller passes `manual=false` or omits the field. The required follow-up for scheduler execution is an explicit `torque_task_update` that sets `manual=false` once the task is ready to run.
