# Session Boot — Clockwork Manifold

## What This Is

Standalone task orchestration and execution engine. Go daemon + CLI + React GUI. All 8 implementation plans complete (478+ tests). GUI entity pages and backend enrichment complete.

## Architecture

- **Daemon** (`cmd/clockworkd/`) — scheduler loop, worker pool, SSE events
- **CLI** (`cmd/clockwork/`) — task management, plugin commands, `serve` for GUI
- **Runtime** — `internal/runtime/` (scheduler, queue, executor, bootstrap)
- **Executor plugins** — `plugins/executor-cli/`, `plugins/executor-api/`
- **Tool system** — `internal/tool/`, `internal/permission/` (4 modes), `internal/sandbox/`, `internal/toolrouter/`
- **Plugin host** — `internal/plugin/` (events, filters, catalog, signatures), `plugins/core/`
- **Concurrency** — `internal/concurrency/` (write serializer, go-queue, DB pool), `internal/worktree/` (git worktrees, merge policies)
- **HTTP + GUI** — `internal/httpserver/` (REST + SSE), `apps/gui/` (React 19, Vite, shadcn/ui, Tailwind v4)
- **Opt-in layers** — `internal/service/` (feature gates, sprint/project/epic services), `internal/mcpadapter/` (14 conditional MCP tools)
- **Storage** — SQLite (WAL mode), `internal/persistence/`

## What Was Done This Session (2026-04-08)

### Backend — Entity Model Enrichment
- Migration 004: added status/icon to projects, project_id to sprints, priority/project_id to epics
- All three entities use active/inactive status (containers are on or off)
- Store layer: ProjectFilter, ProjectUpdate, SprintFilter w/ project_id, EpicFilter w/ project_id
- Service layer: enriched create/update inputs, simplified FSM, project_id links
- REST endpoints: full CRUD for `/projects`, `/sprints`, `/epics` (+ transition for sprints)

### Frontend — Entity Pages + Polish
- Shared components: SummaryCards, PageHeader, DetailHeader, CopyableId, RowActions, ProgressBar
- New pages: ProjectsPage, ProjectDetailPage, SprintsPage, SprintDetailPage, EpicsPage, EpicDetailPage
- Tasks board polished to match Fragments Engine design:
  - Column order: Task | Status | Pri | Date | Actions
  - Executor shown as subtitle under task title
  - Summary cards with accent bars
  - Priority filter enabled
  - Bordered "..." actions button
  - Compact row spacing, aligned checkboxes

### Features Enabled
- features.projects, features.sprints, features.epics all set to true in settings

## What's Next

1. **Test executions** — verify the scheduler/executor pipeline works end-to-end with the enriched models
2. **First plugin: Agents** — the original plan was to add agent support as the first real plugin
3. **GUI iteration** — task detail page needs visual parity, entity pages may need refinement after use
4. **Actions menu** — the "..." button on task rows is currently inert, needs wiring up with status transitions, edit, delete
5. **Projects/Sprints/Epics detail pages** — may need polish passes similar to what tasks board received

## Approach

- TDD: test first, verify fail, implement, verify pass
- Sequential task execution, sonnet for sub-agents if parallelizing
- Cerberus manages services: clockwork-api (port 8990), clockwork-frontend (port 5175)
- After code changes: rebuild clockwork-api via cerberus, restart clockwork-frontend
