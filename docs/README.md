# Docs

This directory now follows a simple rule: current docs should match the code that exists today.

Start with:

- [overview.md](overview.md) — what Clockwork is and what it is not
- [getting-started.md](getting-started.md) — how to run the server and MCP locally
- [runtime.md](runtime.md) — storage, scheduler, executors, and feature flags
- [surfaces.md](surfaces.md) — the current HTTP and MCP surfaces at a high level
- [architecture/sqlite-concurrency-pattern.md](architecture/sqlite-concurrency-pattern.md) — the production SQLite concurrency pattern and reuse guidance
- [hitl-workflows.md](hitl-workflows.md) — typed human-in-the-loop checkpoint workflow contracts
- [architecture/sqlite-test-infra.md](architecture/sqlite-test-infra.md) — why shared SQLite fixtures default to pooled temp-file databases

Older files under `docs/architecture/` and `docs/superpowers/` are historical design notes, working specs, or prior-art research. They are not the canonical source for current behavior unless a fresh doc links to them explicitly.
