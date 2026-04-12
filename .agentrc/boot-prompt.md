# Session Boot — Clockwork Manifold

## Where we left off

**PR #5 (E2E Task Execution — Path A)** merged 2026-04-11. `clockwork serve` now runs the full stack in a single process: HTTP API, scheduler, SSE bridge, and executor registry. Tasks can be created via the API, picked up by the scheduler, executed by mock or real CLI executors, and transitioned through the lifecycle with deliverables gating. Run events are persisted to `run_events` for future live-tailing.

**PR #4 (Sonner Toast Migration)** merged 2026-04-11 by the frontend agent. 16 silent-catch mutation handlers across 9 pages now route errors through `@/lib/toast` module.

## Current architecture (post PR #5)

- **Single process:** `clockwork serve` hosts API (`:8990`), scheduler, SSE bridge, and GUI. No separate daemon.
- **Executor registry:** `mock` (always), `cli` + `api` (via `bootstrap.Executors`). Profiles loaded from `CLOCKWORK_PROFILES_PATH` or `./profiles.yaml` (missing file = empty map, mock-only).
- **Scheduler loop:** picks `todo` non-manual tasks, dispatches to executor, lifecycle manages transitions per `on_done`/`on_fail` rules.
- **Run events:** every execution event persisted to `run_events` table (log, signal, artifact, tokens, progress, task_transitioned, run_started, run_completed). Best-effort — write errors logged, never fail the task.
- **SSE bridge:** `SchedulerBridge` forwards all `EventBus` events to `SSEHub`. GUI SSE clients receive live events.
- **Deliverables gate:** tasks with declared `deliverables` block on missing required artifacts. Verified E2E with mock executor (4 test cases).
- **Test count:** 22 passing packages, 0 failures. Includes 2 serve E2E tests (mock + real CLI smoke), 4 deliverables E2E tests, 3 integration tests, SSE bridge tests with race coverage.

## Path B — iteration-ready loop (next backend session)

Full spec at `docs/superpowers/specs/2026-04-11-e2e-execution-path-b.md`. Path A built the primitives; Path B makes them GUI-observable. Key additions:

1. **Run events HTTP API** — `GET /api/v1/runs/{id}/events?since_id=N&limit=M` for paginated tail. `GET /api/v1/tasks/{id}/runs` already exists.
2. **GUI live updates** — task detail page subscribes to SSE, updates status live, renders run event log, shows artifacts as they arrive.
3. **Real CLI deliverables E2E** — beyond the smoke test; full profile with artifact-gated completion.
4. **`run_completed` on executor-error path** — `TODO(path-b)` breadcrumb at `scheduler.go:270`. Failed runs currently get closure via `task_transitioned` but no explicit `run_completed` event.
5. **Task-scoped SSE filter** — currently client-side; server-side endpoint deferred until network cost justifies.

## Backend follow-ups (standalone, not tied to Path B)

In priority order:

1. **Transition state machine — allow `* → backlog`** from any open status. Surfaced 2026-04-11 during sonner-toast browser verification. `todo → backlog` returns `transition not permitted`. Check transition matrix in `internal/service/task.go`.
2. **Lifecycle SQL error handling** — `retryOrBlock` and `handleEscalation` silently swallow `QueryRow().Scan()` errors on `retry_count` / `escalation_step`. A failed Scan defaults to 0, causing incorrect retry/block decisions. Flagged by Copilot in PR #5 review as pre-existing.
3. **`ArtifactRecord` JSON casing** — PascalCase field names in API response (`Type`, `Content`) vs expected snake_case. Pre-existing; only visible now that artifacts are populated by the CLI path.
4. **`applyDefaults` MaxRetries coercion** — `MaxRetries=0` coerced to `3` in `sqlstore/tasks.go:119-121`. Makes it impossible to set zero retries via API. Workaround: raw SQL in tests. Consider nullable sentinel.
5. **PUT vs PATCH harmonization** — task update uses PUT, tag endpoints use PATCH.
6. **`taskJSON` timestamp serialization** — raw `time.Time` vs `.Format(time.RFC3339)` in `tagJSON`.
7. **N+1 task-tag loading in `tasksJSON`** — batch the query.
8. **Task list endpoint filters** on Project-2 fields (currently only status, priority, sprint_id, project_id, epic_id, executor).
9. **`.agentrc` docs drift** — historical specs/plans still reference `clockworkd`. Non-blocking; update opportunistically.

## Deferred (not in scope for near-term sessions)

NavArrows + context-aware back-links (wait until all 4 detail pages are on HUD); Tier 5 field editors (permissions/metadata) need a real design pass; other detail page HUD rebuilds; inline field-level edit conveniences; `@mention` task ID picker; tag management GUI (`BLG-20260409-002`); duplication extraction from Tasks 5–8 section helpers.

## Environment notes

- `GOPRIVATE=github.com/hollis-labs/*` is already set in this dev env.
- Cerberus manages `clockwork-api` (8990) and `clockwork-frontend` (5175). After backend changes, rebuild via cerberus and restart.
- Stale LSP diagnostics during fast subagent loops are common — verify real state via `go test` / `npm run build` rather than trusting compiler-error system reminders.
- Worktrees cleaned up. No active feature branches.
