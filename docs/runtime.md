# Runtime

## Storage

Torque opens either:

- SQLite, when `TORQUE_POSTGRES_DSN` is not set
- Postgres, when `TORQUE_POSTGRES_DSN` is set

Schema migrations run on startup for both `serve` and `mcp`.

## Main Runtime Pieces

- Store: canonical task and related entity persistence
- Queue: scheduler job queue stored separately under the runtime data dir
- Service layer: validation and business logic
- Scheduler: dispatch loop for runnable tasks
- Executor registry: registered execution backends
- Waitpoll registry: predicate-based dispatch support for `kind=wait`
- HTTP server: REST API, SSE, and GUI
- MCP adapter: stdio MCP surface over the service layer

## Scheduler

The scheduler is created only in `torque serve`.

Current behavior:

- Polls on an interval
- Tracks worker heartbeats
- Supports session-scoped enable/disable
- Exposes read-only live state to stdio MCP clients via `torque_scheduler_status`, which proxies `torque serve`'s `/api/v1/scheduler/status` when no in-process scheduler exists
- Applies a max-per-project concurrency limit
- Can run with optional per-run worktrees
- Uses a project allowlist stopgap from env vars today

Important env vars:

- `TORQUE_SCHED_WORKERS`
- `TORQUE_SCHED_INTERVAL`
- `TORQUE_SCHED_ENABLED`
- `TORQUE_SCHED_MAX_PER_PROJECT`
- `TORQUE_SCHED_STALE`
- `TORQUE_WORKTREE_PER_RUN`
- `TORQUE_WORKTREE_ROOT`
- `TORQUE_WORKTREE_KEEP_DAYS`
- `TORQUE_PROJECT_ID`
- `TORQUE_PROJECT_IDS`

## Feature Flags

Projects, sprints, and epics are opt-in features. They are exposed through settings and checked by both HTTP and MCP layers.

Current feature keys:

- `features.projects`
- `features.sprints`
- `features.epics`

If a feature is off, the related service or tool returns a feature-disabled error rather than silently approximating behavior.
