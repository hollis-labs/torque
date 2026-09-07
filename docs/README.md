# Docs

This directory now follows a simple rule: current docs should match the code that exists today.

Start with:

- [overview.md](overview.md) — what Torque is and what it is not
- [getting-started.md](getting-started.md) — how to run the server and MCP locally
- [runtime.md](runtime.md) — storage, scheduler, executors, and feature flags
- [surfaces.md](surfaces.md) — the current HTTP and MCP surfaces at a high level
- [mcp-tools-reference.md](mcp-tools-reference.md) — per-tool index and shared conventions (envelope, pagination, sort, bulk) for the Task/Subtodo/Comment/Project/Epic/Sprint/Issue/Plan MCP tools
- [architecture/sqlite-concurrency-pattern.md](architecture/sqlite-concurrency-pattern.md) — the production SQLite concurrency pattern and reuse guidance
- [hitl-workflows.md](hitl-workflows.md) — typed human-in-the-loop checkpoint workflow contracts
- [agent-execution-environment.md](agent-execution-environment.md) — the boot dir, planted task bundle, per-run worktree, and permission-mode contract for orchestrated agent runs
- [aar-system.md](aar-system.md) — After-Action Report schema, submission protocol, and `torque aar` query CLI
- [architecture/sqlite-test-infra.md](architecture/sqlite-test-infra.md) — why shared SQLite fixtures default to pooled temp-file databases

Directional and exploratory:

- [work-coordination-direction.md](work-coordination-direction.md) — a 2026-08-22
  target-direction draft on Torque's place in the portfolio. Draft status; not
  a contract.
- [vnext/](vnext/) — a 2026-09-07 vNext architecture sketch and the context that
  produced it. Exploration; nothing in it is decided or scheduled.

Older files under `docs/architecture/` and `docs/superpowers/` are historical design notes, working specs, or prior-art research. They are not the canonical source for current behavior unless a fresh doc links to them explicitly.
