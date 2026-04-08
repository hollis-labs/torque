# Clockwork Manifold — Session Boot Prompt

## Context
Building Clockwork Manifold at `~/Projects-apps/clockwork-manifold` — a standalone task orchestration + execution engine. Plans and spec live in the engine repo at `~/Projects-apps/fragments-engine/engine/docs/superpowers/`.

## Current State (2026-04-08)
- **Plans 1-2:** Complete (foundation, scheduler, queue — 83 tests)
- **Plan 3 Tasks 1-7:** Complete (signal protocol, parsers, env filtering, agent profiles)
- **Plan 3 Tasks 8-12:** Next up

## What's Next: Plan 3 Tasks 8-9 (Executor Plugins)
Read the plan at: `~/Projects-apps/fragments-engine/engine/docs/superpowers/plans/2026-04-07-plan-3-executor-system.md`

- **Task 8: executor-cli Plugin** (line ~1931) — `plugins/executor-cli/` — spawns CLI processes for claude/codex/copilot/gemini/generic, parses stdout through signal parser, env filtering, optional PTY
- **Task 9: executor-api Plugin** (line ~2843) — `plugins/executor-api/` — calls LLM APIs via go-providers, conversation loop with tool calling (stubbed until Plan 4)
- **Task 10:** Plugin registration wiring
- **Task 11:** Integration test
- **Task 12:** Final verification

## Key Files
- `internal/runtime/executor/` — Executor interface, types, signals, parsers, env filtering, registry, mock (64 tests)
- `internal/config/profiles.go` — AgentProfile YAML config (CLI + API profiles)
- `internal/runtime/scheduler/` — Scheduler loop, worker pool
- `internal/runtime/queue/` — SQLite job queue

## Approach
- Boot as `engine-backend` agent
- TDD: write test first, verify fail, implement, verify pass, commit
- Sequential task execution, sonnet for subagents if parallelizing
