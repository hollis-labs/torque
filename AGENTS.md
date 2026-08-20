# Torque — Agent Orientation

## What is this and why

Torque is a local-first task orchestration and execution engine for agent work.
It is a clean rebuild of Fragments Engine's core — task FSM + scheduler + executor
+ MCP — stripped to a single purpose and made plugin-first. Tasks move through a
finite-state machine (`todo` → `doing` → `review` → `done`), are dispatched from a
persistent queue, and are run by pluggable executor backends. Everything beyond
durable task management and execution is a plugin. Torque is planned to replace
Fragments Engine / Volon / Engine MCP once adoption stabilizes; it is *not* the old
Fragments Engine codebase renamed in place, and it is *not* a general
project-management suite or generic workflow engine.

> Naming note: the project was renamed Clockwork → Torque (commit `4292ca3`). The
> Go module is `github.com/hollis-labs/torque`, the binary is `torque`, the state
> dir is `~/.torque`, and MCP tools use the `torque_*` prefix. `clockwork-manifold`
> is a backward-compat symlink to this repo. You will still see `clockwork`/
> `CLOCKWORK_*` in historical docs and some retained env-var names — treat the
> `torque` forms as canonical.

## Where to start

- `README.md` — top-level orientation and configuration env vars.
- `docs/README.md` — docs index. Current docs match the code that exists today;
  older files under `docs/architecture/` and `docs/superpowers/` are historical
  design notes, not the current contract.
- `docs/overview.md` — what Torque is and is not.
- `docs/getting-started.md` — running the server and MCP locally.
- `docs/runtime.md` — storage, scheduler, executors, feature flags.
- `docs/surfaces.md` — the current HTTP and MCP surfaces.
- `docs/mcp-tools-reference.md` — per-tool index for the Task/Subtodo/Comment/
  Project/Epic/Sprint/Issue/Plan MCP tools: shared envelope/pagination/sort/
  bulk conventions plus a table per entity. Points back into
  `internal/mcpadapter/*.go` for exact schemas rather than duplicating them.
- `cmd/torque/` — CLI + `serve` (HTTP API + scheduler + GUI) + `mcp` command host.
- `cmd/torque-apikey-helper/` — OAuth/keychain API-key helper used by executors.
- `internal/service/` — task CRUD, validation, lifecycle rules, tag management.
- `internal/persistence/sqlstore/` — SQLite store + migrations.
- `internal/runtime/scheduler/` — queue + worker pool, persistent `run_events`.
- `internal/runtime/cliexec/` — `cli` executor (claude/codex/gemini/copilot/opencode).
- `internal/runtime/executor/` — executor plugin interface (`Run`, `Capabilities`, `Validate`).
- `internal/httpserver/` — REST API with strict validation; SSE for live observability.
- `internal/mcpadapter/` — `torque_*` MCP tools for harnesses.
- `apps/gui/` — React 19 + Vite + Tailwind 4 + shadcn GUI (served by `torque serve`).
- `CLAUDE.md` — agent/operator notes for this repo.

## Key domain concepts

- **Task record** — the canonical 35-field task object, surfaced over HTTP with
  strict validation. Budget fields use sentinel values: `null` = inherit, `-1` =
  unlimited, `0` = explicit zero. Deliverables (13 typed kinds) are first-class.
- **FSM + scheduler** — tasks transition `todo → doing → review → done`. The
  scheduler dispatches runnable tasks from a persistent queue to registered
  executors and persists `run_events` for observability.
- **Executors** — pluggable backends behind an interface. Core ships a `cli`
  executor (drives provider CLIs via go-providers adapters) and an `api` executor
  (vendor SDKs — Anthropic, OpenAI; Gemini deferred). `executor` defaults to
  `cli`.
- **Per-task MCP loopback** — agent runs talk back via an ephemeral
  `127.0.0.1:0` HTTP MCP server whose tool catalog is closure-bound to the
  spawning task (interpretive transitions: `torque_task_blocked`, `_review`,
  `_summary`, `_subtodo_done`, `_checkpoint_emit`, etc.).
- **Feature flags** — `project`, `sprint`, `epic`, and `collection` entities and
  their MCP tool groups are opt-in via persisted `features.*` settings. Restart
  the MCP process after toggling a flag so `tools/list` refreshes.
- **Plugins** — `executor-api` and others live under `plugins/`; the plugin
  contract is the same `hollis-labs/go-plugin` SDK Nanite uses.
- **Relational tags** — tags are a relational, ordered, race-free join table
  (not JSON). Backlog is modelled as a tag.

## Common operations

```bash
# Build the binaries (torque + torque-apikey-helper)
make build

# Run the HTTP API + scheduler + GUI locally (default port 8990)
torque serve

# Run the MCP server over stdio for a harness
torque mcp

# Tests
make test               # full backend suite: go test ./... -v -count=1
make test-scheduler     # scheduler package only
make lint               # go vet ./...
```

Configuration (env vars): `TORQUE_DB_PATH` (SQLite path, default `torque.db`),
`TORQUE_POSTGRES_DSN` (use Postgres instead of SQLite when set),
`TORQUE_HTTP_PORT` (default `8990`), `TORQUE_DATA_DIR` (default `.torque`),
`TORQUE_PROFILES_PATH` (optional agent profile YAML).

**Build/deploy authority is Cerberus, not git.** The production-shape service is
the `torque-api-service` Cerberus resource (launchd-managed,
`com.fragments-engine.cerberus.torque.torque-api-service`, port 8990, runs from a
synced artifact). Deploy with `cerberus_resource_deploy torque-api-service`. Dev
resources also exist: `torque-api-dev` (port 8992), `torque-scheduler`,
`torque-frontend` (Vite, 5175), `torque-frontend-prod` (Vite, 5180).

> Do not commit/push as part of routine agent work in this repo unless explicitly
> asked. Build/deploy go through Cerberus.

## Where to look for more

- `docs/architecture/` — design notes: `task-model-v0.1.md`, `design-philosophy.md`
  (agent-first principles ported from Fragments Engine),
  `sqlite-concurrency-pattern.md`, `mcp-registry.md`, `special-agent-patterns.md`,
  `unified-messaging-architecture.md`.
- `docs/hitl-workflows.md` — typed human-in-the-loop checkpoint contracts.
- `docs/superpowers/specs/` and `docs/superpowers/plans/` — working specs and
  historical plans (e.g. the Torque design spec and the executor-wiring plan).
- Project knowledge file: `~/dev/agent-os/knowledge/projects/torque.md` — the
  fullest narrative of lineage, architecture, GAPs, and the SQLite concurrency
  ceiling.
- Tracking artifacts: `~/dev/agent-os/workspaces/execution/torque/`.
- `.agent-ops/project.yaml` — the structured Source-of-Truth for this project.

### Known constraints worth knowing

- **SQLite concurrency ceiling.** Torque starts on SQLite for zero-ops local-first
  UX, but the predecessor (Fragments Engine) hit deadlocks under sustained agent
  load even with WAL and had to move to Postgres. Torque has already seen
  `SQLITE_BUSY` at trivial parallelism. A durable storage strategy (embedded
  Postgres / libSQL / DuckDB / hybrid tier) is still being chosen — don't
  over-invest in deep SQLite tuning. Postgres is available today via
  `TORQUE_POSTGRES_DSN`.
- **Executor → tool registry not yet wired.** `SupportsTools` is honestly `false`
  on both `cli` and `api` executors; tool-broker enforcement waits on "Plan 4".
- **Sandbox restoration deferred to Phase F** (blocked on a go-sandbox
  `AllowLoopback` knob so the per-task MCP loopback survives `Net=false`).
