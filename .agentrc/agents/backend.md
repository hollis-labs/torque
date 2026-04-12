# Backend Context — Clockwork Manifold

> Project-specific backend conventions. Loaded by the backend agent role when working in this project.

## Current State

Active development. Go backend with SQLite storage. Single-process architecture: `clockwork serve` hosts API, scheduler, SSE bridge, and GUI.

## Stack

- **Language:** Go 1.26
- **Storage:** SQLite (WAL mode)
- **CLI:** `cmd/clockwork/` (client commands + `serve` hosting everything)
- **Build:** `make` (see Makefile)

## Project Structure

```
clockwork-manifold/
├── cmd/
│   └── clockwork/       # CLI client + serve command
├── internal/
│   ├── runtime/         # Scheduler, queue, executor
│   │   ├── scheduler/   # Picker, lifecycle, worker pool, event bus, run_events
│   │   ├── executor/    # Executor interface, types, signal parsing
│   │   ├── queue/       # Hot-write queue DB
│   │   └── bootstrap/   # Executor registry initialization
│   ├── httpserver/      # HTTP API, SSE hub, scheduler bridge
│   ├── config/          # Agent profiles, YAML config
│   ├── service/         # Business logic layer
│   └── persistence/     # SQLite store, migrations
├── plugins/             # Executor plugins (cli, api)
├── apps/gui/            # React frontend
├── go.mod
└── Makefile
```

## Patterns

- TDD: write test first, verify fail, implement, verify pass
- Internal packages under `internal/` — not importable externally
- Plugin-based executor system in `plugins/`
- Run events are best-effort observability: write errors logged, never fail tasks
- `handleArtifactSignal` centralized in executor-cli for one-source-of-truth

## Key files for scheduler/execution work

- `cmd/clockwork/serve.go` — `runServe(ctx, ln)` is the context-aware core
- `internal/runtime/scheduler/scheduler.go` — tick loop, dispatch, drain
- `internal/runtime/scheduler/lifecycle.go` — `HandleResult`, transitions, deliverables gate
- `internal/runtime/scheduler/run_events.go` — `writeRunEvent` helper
- `internal/httpserver/sse_bridge.go` — EventBus → SSEHub adapter
- `internal/httpserver/settings.go` — real scheduler status/toggle handlers
- `plugins/executor-cli/plugin.go` — CLI executor with signal parsing

## Notes

- Plans and specs may reference `~/Projects-apps/fragments-engine/engine/docs/superpowers/` for historical context
- Prior art survey at `docs/superpowers/specs/2026-04-11-e2e-execution-prior-art.md`
