# Surfaces

This page is intentionally high level. It lists the major live surfaces without trying to exhaustively duplicate schemas already enforced in code and tests.

## HTTP API

Base path: `/api/v1`

Main route groups:

- `/tasks`
- `/checkpoints`
- `/checkpoint-workflows` for HITL checkpoint schema lookup
- `/templates`
- `/projects`
- `/sprints`
- `/epics`
- `/plans`
- `/tags`
- `/runs`
- `/artifacts`
- `/comments`
- `/settings`
- `/scheduler`
- `/features`
- `/admin`
- `/events` for SSE

The same `serve` process also serves the GUI SPA.

Task-list query contract (`GET /api/v1/tasks`): omitted filters are
unfiltered/defaulted, while malformed explicit values return `400` with
`error` and `field`. `priority` is an exact integer filter and may be a
comma-separated OR-list (`priority=0,2`) for GUI compatibility; `0` is a real
priority value, not omission. Repeated query keys, malformed query strings,
blank/overflow/non-integer priority members, malformed `limit`/`offset`,
invalid `manual`/`include_internal` aliases, unknown keys, and MCP-only keys
that HTTP does not implement yet all return `400` rather than being ignored.

## MCP

Core MCP areas exposed by the adapter:

- Health
- Tasks
- Runs
- Artifacts
- Comments
- Settings
- Scheduler
- Checkpoints
- Templates
- Subtodos
- Plans
- Issues

Feature-flagged MCP areas:

- Projects
- Sprints
- Epics
- Collections

The MCP adapter is thin over the service layer. The code under `internal/mcpadapter/` is the authoritative place to inspect current tool names and descriptions.

Task-list MCP query contract (`torque_task_list`): explicit malformed
numbers, booleans, dates, directions, cursors, and wrong native types return
`error.code=arg_invalid` with `error.field`; omitted values remain
unfiltered/defaulted. Priority filters are exact arbitrary integers, including
`0`. Use scalar `priority` for one value or `priorities` as a non-empty JSON
array for OR-matching; passing both is rejected as ambiguous, and
`priorities=[]` is rejected.

For the eight data-management entities (Task, Subtodo, Comment, Project, Epic, Sprint, Issue, Plan), [mcp-tools-reference.md](mcp-tools-reference.md) is a per-tool index plus the shared conventions (response envelope, pagination, sort, bulk-op shape) — the "MCP discoverability/schema reference layer" [ADR-0004](adr/0004-mcp-data-entity-agent-ergonomics.md) scoped and deferred.

## GUI

The frontend currently includes pages for:

- Dashboard
- Board / operations
- Tasks
- Runs
- Checkpoints
- Templates
- Plans
- Settings
- Project detail
- Sprint detail
- Epic detail

The GUI is served by the same `torque serve` process.
