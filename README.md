# Clockwork Manifold

Clockwork Manifold is a task orchestration runtime for agent work.

Today it provides:

- A persistent task store backed by SQLite by default, with Postgres support behind `CLOCKWORK_POSTGRES_DSN`
- An HTTP API plus GUI via `clockwork serve`
- An MCP server via `clockwork mcp`
- A scheduler that dispatches runnable tasks to registered executors
- First-class support for artifacts, comments, subtodos, checkpoints, templates, and plans
- Optional project, sprint, and epic entities behind feature flags

Clockwork is still early. The docs in this repo are intentionally light and only cover the surfaces that currently exist in code.

## Start Here

- Repo docs index: [docs/README.md](docs/README.md)
- Agent/operator notes: [CLAUDE.md](CLAUDE.md)

## Commands

```bash
clockwork serve
clockwork mcp
clockwork version
```

`clockwork serve` starts the HTTP API, scheduler, and GUI on the configured HTTP port.

`clockwork mcp` starts the MCP server over stdio. In that mode there is no in-process scheduler instance, so scheduler MCP tools report that the scheduler is not running in that process.

## Configuration

Common environment variables:

- `CLOCKWORK_DB_PATH` — SQLite DB path, default `clockwork.db`
- `CLOCKWORK_POSTGRES_DSN` — if set, use Postgres instead of SQLite
- `CLOCKWORK_HTTP_PORT` — HTTP port for `serve`, default `8990`
- `CLOCKWORK_DATA_DIR` — runtime data dir, default `.clockwork`
- `CLOCKWORK_PROFILES_PATH` — optional agent profile YAML path

Scheduler-related settings are documented in [docs/runtime.md](docs/runtime.md).
