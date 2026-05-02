# Runtime

## Storage

Clockwork opens either:

- SQLite, when `CLOCKWORK_POSTGRES_DSN` is not set
- Postgres, when `CLOCKWORK_POSTGRES_DSN` is set

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

The scheduler is created only in `clockwork serve`.

Current behavior:

- Polls on an interval
- Tracks worker heartbeats
- Supports session-scoped enable/disable
- Applies a max-per-project concurrency limit
- Can run with optional per-run worktrees
- Uses a project allowlist stopgap from env vars today

Important env vars:

- `CLOCKWORK_SCHED_WORKERS`
- `CLOCKWORK_SCHED_INTERVAL`
- `CLOCKWORK_SCHED_ENABLED`
- `CLOCKWORK_SCHED_MAX_PER_PROJECT`
- `CLOCKWORK_SCHED_STALE`
- `CLOCKWORK_WORKTREE_PER_RUN`
- `CLOCKWORK_WORKTREE_ROOT`
- `CLOCKWORK_WORKTREE_KEEP_DAYS`
- `CLOCKWORK_PROJECT_ID`
- `CLOCKWORK_PROJECT_IDS`

## Feature Flags

Projects, sprints, and epics are opt-in features. They are exposed through settings and checked by both HTTP and MCP layers.

Current feature keys:

- `features.projects`
- `features.sprints`
- `features.epics`

If a feature is off, the related service or tool returns a feature-disabled error rather than silently approximating behavior.
