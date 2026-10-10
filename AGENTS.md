# Torque

Torque is a local-first task orchestration engine: a task FSM, a persistent
queue and scheduler, pluggable executors, and HTTP + MCP surfaces over one
SQLite or Postgres store. It dispatches work and records what happened. It is
not a project-management suite, a workflow engine, or an agent runtime — it does
not host the agent it dispatches to. It was renamed from Clockwork, so `CW-`
task ids and `clockwork` in older docs and code comments are historical.

## Start Here

- `docs/README.md` indexes current docs. `docs/architecture/` is current
  reference; it is not the contract.
- Older docs and code comments may cite planning and design-history paths that
  are no longer in this repo; those paths do not resolve and are not current
  contract.
- `internal/service/task.go` owns the status vocabulary (`CanonicalStatuses`)
  and the transition policy (`checkTransition`).
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
make build-prod      # embeds the GUI into the binary; make build omits it
make install         # build-prod, then installs to BINDIR (default ~/go/bin)
```

`make install BINDIR=$HOME/.local/bin` installs elsewhere. `make gui` installs
with `npm ci`, so it never rewrites `apps/gui/package-lock.json`; after changing
dependencies, run `npm install` in `apps/gui` and commit the lockfile it writes.

`make profiles-lint` validates `~/.config/torque/profiles.yaml`, operator state
rather than a repo file, so it can fail on a clean checkout.

## Boundaries

`torque serve` binds `127.0.0.1:<TORQUE_HTTP_PORT>` by default and refuses a
non-loopback `--addr` unless `TORQUE_API_TOKEN` is set; with a token, every
`/api` request needs `Authorization: Bearer`. The policy lives in
`internal/httpserver/security.go`. New routes go under `/api/v1` so they inherit
it, and a new HTTP client of Torque must send the token when one is configured
(see `proxySchedulerStatus`). See `SECURITY.md`.

Task transitions are permissive: any status in
`CanonicalStatuses` reaches any other in one call, so nothing has to walk a
path to record what already happened. Exactly two things are refused, and each
error names its own remedy — a status outside the vocabulary, and leaving the
terminal statuses `done`/`archived`. `ForceTransition` clears the second only;
it does NOT bypass the vocabulary, because a typo under force is how the store
accumulated hundreds of rows the old FSM had no key for.

Add a status to `CanonicalStatuses` and to the GUI's `TaskStatus` union
together — they were allowed to drift apart once and produced three
disagreeing vocabularies.

SQLite write ownership is explicit and already solved: handles come from
`appdb.Open`, and `sqlstore.New` re-opens them as a single-connection writer
plus a bounded reader. Do not add another write path or answer `SQLITE_BUSY`
with `SetMaxOpenConns(1)`. Storage remains an open decision. SQLite is the
supported application backend; Postgres migrations remain, but application
startup guards the incomplete CRUD adapter unless an explicit development
override is set.

Migrations are keyed by filename and applied once, so editing a landed one
changes nothing on an existing database. Add a new numbered file. Feature flags
(`projects`, `sprints`, `epics`, `collections`) are read at MCP process start;
restart it after toggling one or `tools/list` will not change.
