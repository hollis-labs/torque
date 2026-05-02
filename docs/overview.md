# Overview

Clockwork Manifold is a task orchestration runtime for agent work.

What exists today:

- Persistent task records with lifecycle transitions
- Scheduler-driven dispatch for runnable tasks
- Executor registry and bootstrap path
- HTTP API and browser GUI from the same `serve` process
- MCP adapter for task and related operations
- Supporting entities: artifacts, comments, subtodos, checkpoints, templates, runs, plans
- Optional project, sprint, and epic support behind feature flags

What Clockwork is not:

- It is not a general project-management suite
- It is not a generic workflow engine for every product in the portfolio
- It is not the old Fragments Engine codebase renamed in place

The current implementation is a clean Clockwork codebase with some historical design notes still present in `docs/architecture/` and `docs/superpowers/`. Those notes are useful as background, but they should not be treated as the current contract.
