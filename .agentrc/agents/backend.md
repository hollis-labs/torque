# Backend Context — Clockwork Manifold

> Project-specific backend conventions. Loaded by the backend agent role when working in this project.

## Current State

Active development. Go backend with SQLite storage. Daemon (`clockworkd`) exposes API, CLI (`clockwork`) is the client.

## Stack

- **Language:** Go 1.26
- **Storage:** SQLite (WAL mode)
- **CLI:** `cmd/clockwork/` (client commands)
- **Daemon:** `cmd/clockworkd/` (server process)
- **Build:** `make` (see Makefile)

## Project Structure

```
clockwork-manifold/
├── cmd/
│   ├── clockwork/       # CLI client
│   └── clockworkd/      # Daemon server
├── internal/
│   ├── runtime/         # Scheduler, queue, executor
│   ├── config/          # Agent profiles, YAML config
│   └── ...
├── plugins/             # Executor plugins
├── clockwork.db         # SQLite database
├── go.mod
└── Makefile
```

## Patterns

- TDD: write test first, verify fail, implement, verify pass
- Internal packages under `internal/` — not importable externally
- Plugin-based executor system in `plugins/`

## Notes

- Plans and specs may reference `~/Projects-apps/fragments-engine/engine/docs/superpowers/` for historical context
