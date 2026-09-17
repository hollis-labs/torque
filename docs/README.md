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
- [review-routing.md](review-routing.md) — per-task review routing metadata and the `effective_review` read contract
- [agent-execution-environment.md](agent-execution-environment.md) — the boot dir, planted task bundle, per-run worktree, and permission-mode contract for orchestrated agent runs
- [aar-system.md](aar-system.md) — After-Action Report schema, submission protocol, and `torque aar` query CLI
- [plan-branch-strategies.md](plan-branch-strategies.md) — branch/PR strategies for `kind=plan` programs, and the mandatory terminal reconcile-and-build step for option-4 (shared-branch) plans
- [architecture/sqlite-test-infra.md](architecture/sqlite-test-infra.md) — why shared SQLite fixtures default to pooled temp-file databases

Older files under `docs/architecture/` and `docs/superpowers/` are historical design notes, working specs, or prior-art research. They are not the canonical source for current behavior unless a fresh doc links to them explicitly.
