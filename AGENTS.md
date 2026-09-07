# Torque

Torque is a local-first task orchestration engine: a task FSM, a persistent
queue and scheduler, pluggable executors, and HTTP + MCP surfaces over one
SQLite or Postgres store. It dispatches work and records what happened. It is
not a project-management suite, a workflow engine, or an agent runtime — it does
not host the agent it dispatches to. Renamed Clockwork → Torque, so `CW-` task
ids and `clockwork` in older docs are historical.

## Start Here

- `docs/README.md` indexes current docs. `docs/architecture/` and
  `docs/superpowers/` are historical notes, not the contract.
- `internal/service/task.go` owns the status FSM (`validTransitions`).
- `internal/persistence/sqlstore/store.go` splits one handle into a serialized
  writer and a bounded reader; `migrations/` holds the numbered SQL.
- `internal/runtime/scheduler/` dispatches from the queue;
  `internal/runtime/agent/` is the `cli` executor and its boot path.
- `internal/mcpadapter/` defines the `torque_*` tools, indexed by
  `docs/mcp-tools-reference.md`.
- `cmd/torque/serve.go` and `cmd/torque/mcp.go` are the process entry points.
- `apps/gui/` is the React GUI, embedded by `make build-prod`.

## Commands

```bash
make lint            # go vet ./...
make test            # full backend suite
make test-scheduler  # ./internal/runtime/... only
make build-prod      # embeds the GUI; what Cerberus builds. make build omits it
```

`make profiles-lint` validates `~/.config/torque/profiles.yaml`, operator state
rather than a repo file, so it can fail on a clean checkout.

## Boundaries

Build and deploy authority is Cerberus, not git. Production is the
`torque-api-service` resource — launchd, port 8990, run from a synced artifact,
deployed with `cerberus_resource_deploy torque-api-service`.

`TaskService.Transition` enforces the FSM. `ForceTransition` bypasses it for
user-initiated dispositioning only, never to make an inconvenient transition
legal.

SQLite write ownership is explicit and already solved: handles come from
`appdb.Open`, and `sqlstore.New` re-opens them as a single-connection writer
plus a bounded reader. Do not add another write path or answer `SQLITE_BUSY`
with `SetMaxOpenConns(1)`. Storage remains an open decision and Postgres works
today via `TORQUE_POSTGRES_DSN`, so deep SQLite tuning is wasted work.

Migrations are keyed by filename and applied once, so editing a landed one
changes nothing on an existing database. Add a new numbered file. Feature flags
(`projects`, `sprints`, `epics`, `collections`) are read at MCP process start;
restart it after toggling one or `tools/list` will not change.
