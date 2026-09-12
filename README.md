# Torque

Torque is a task orchestration runtime for agent work.

Today it provides:

- A persistent task store backed by SQLite by default, with Postgres support behind `TORQUE_POSTGRES_DSN`
- An HTTP API plus GUI via `torque serve`
- An MCP server via `torque mcp`
- A scheduler that dispatches runnable tasks to registered executors
- First-class support for artifacts, comments, subtodos, checkpoints, templates, and plans
- Optional project, sprint, epic, and collection entities behind feature flags

Torque is still early. The docs in this repo are intentionally light and only cover the surfaces that currently exist in code.

## Start Here

- Repo docs index: [docs/README.md](docs/README.md)
- Agent/operator notes: [CLAUDE.md](CLAUDE.md)

## Commands

```bash
torque serve
torque mcp
torque profiles lint
torque version
```

`torque serve` starts the HTTP API, scheduler, and GUI on the configured HTTP port.

The standalone Vite frontend (`cd apps/gui && npm run dev`, or the Cerberus
resource `torque-frontend-dev`) uses port **5182** and refuses to silently move
to another port if it is occupied. The Cerberus resource proxies API requests
to the dev API on 8992; a direct Vite launch defaults to the API on 8990 unless
`TORQUE_GUI_API_ORIGIN` is set.

`torque mcp` starts the MCP server over stdio. In that mode there is no in-process scheduler instance, so scheduler MCP tools report that the scheduler is not running in that process.
Opt-in MCP tool groups are registered when the MCP process starts, based on persisted `features.*` settings in the backing DB. If you enable a new feature such as `features.collections`, restart the MCP process so `tools/list` picks up the new `torque_collection_*` tools.

`torque profiles lint` validates `profiles.yaml` as an execution-template registry against Torque's current executor/provider catalog. It fails on unknown fields, missing or unsupported providers, and dishonest profile names that omit or misstate the provider binding. Use `make profiles-lint` in CI or pre-commit.

## Configuration

Common environment variables:

- `TORQUE_DB_PATH` — SQLite DB path, default `torque.db`
- `TORQUE_POSTGRES_DSN` — if set, use Postgres instead of SQLite
- `TORQUE_HTTP_PORT` — HTTP port for `serve`, default `8990`
- `TORQUE_DATA_DIR` — runtime data dir, default `.torque`
- `TORQUE_PROFILES_PATH` — optional agent profile YAML path

Scheduler-related settings are documented in [docs/runtime.md](docs/runtime.md).
