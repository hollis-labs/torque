# Session Boot — Clockwork Manifold

## What This Is

Standalone task orchestration and execution engine. Go daemon + CLI + React GUI. All 8 implementation plans complete (478+ tests). GUI entity pages and backend enrichment complete. Tag system shipped (Project 1, PR #1). Task API expansion shipped (Project 2, PR #2). Next up: task detail/edit page rebuild (Project 3).

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
- **Tags** — first-class relational entity, `internal/persistence/sqlstore/tags.go`, `internal/service/tag.go`, `/api/v1/tags` CRUD + merge
- **Task validation** — shared `validateTaskWrites` helper in `internal/service/task_validation.go` enforces enum/sentinel/dependency rules on both Create and Update

## Three-Project Roadmap

The work over recent sessions has been a chain leading toward a proper task detail/edit page:

1. ✅ **Tag system (PR #1)** — relational tags with CRUD/merge, auto-create on write, color palette, frontend `<TagChip>`. Committed.
2. ✅ **Task API expansion (PR #2)** — surface 12 missing canonical task fields through HTTP, strict validation (enums, sentinels, dependency existence, deliverable types), `TaskCreateRequest`/`TaskUpdateRequest` typed handlers, status field rejection on update, narrowed TS lifecycle types, sentinel constant `Unlimited`. Committed.
3. ⏭ **Task detail/edit page rebuild (next)** — replace the current `TaskDetailPage` with one that matches the tasks home visual language, supports inline edit via query param, and ports the context-aware `NavArrows` component from Fragments Engine.

## What Was Done This Session (2026-04-09)

### Project 1 — Tag system (PR #1, merged)

- Migration `005_tags.sql`: `tags` table (slug PK, name, description, color CHECK, timestamps), `task_tags` join table (composite PK, sort_order, FK CASCADE both sides), drops legacy `tasks.tags` column
- Store layer: `TagRecord`, CRUD, `SetTaskTags`/`ListTaskTags` (transactional, sort_order ordered), `MergeTags` with dedupe semantics, `ErrTagNotFound` sentinel
- Service layer: `TagService` with validation (palette, slug rules), `Merge`, `ResolveNames` (auto-create on write, race-free via `CreateTagIfNotExists`)
- HTTP: `/api/v1/tags` CRUD + `POST /tags/:slug/merge`, error mapping with 409 on duplicate, 404 via sentinel detection
- Task API contract: task responses now include structured `Tag[]`; create/update accept `tags: []string` (auto-resolved by name)
- MCP adapter: `taskWithTags` helper restores tags inline on single-task tool outputs
- Frontend: `Tag`/`TagColor`, `TAG_COLOR_CLASSES`, reusable `<TagChip>` component, board row 3-cap + `+N more` overflow, task detail uncapped, removed legacy `parseTags`
- External module: `github.com/hollis-labs/go-strutil` (provides `Slugify` plus 18 other Laravel-style helpers)
- Worktree resolver: merge-resolution auto-tasks re-attach `merge-resolution` + `auto-generated` tags
- Backlog captured: `BLG-20260409-001` (Deliverables in API), `BLG-20260409-002` (tag management GUI, post-MVP)

### Project 2 — Task API expansion (PR #2, merged)

- 12 canonical task fields surfaced through HTTP read + write: `tools`, `permissions`, `environment`, `files`, `max_duration_ms`, `token_budget`, `escalation_chain`, `quality_gates`, `deliverables`, `deliverable_preset`, `depends_on`, `metadata`
- New `service.Deliverable` type, `Unlimited` sentinel constant (untyped, works for int64 + float64)
- Service layer: `validateTaskWrites` shared helper used by both `Create` and `Update`; enum validation for `on_done`/`on_fail`/`on_review`/`on_done_merge`; numeric bounds with sentinel support (`-1` = unlimited, `0` = explicit zero for cost only, positive = specific); deliverable type validation; depends_on existence check via `errors.Is(err, sqlstore.ErrTaskNotFound)`
- HTTP layer: `TaskCreateRequest`/`TaskUpdateRequest` named typed structs replace inline anonymous + map-based parsing; `taskJSON` expanded with all 12 fields via 4 new parse helpers (`parseStringArray`/`parseStringMap`/`parseFreeMap`/`parseDeliverables`); `nullJSONString` write helper; status field rejection on update returns 400 pointing to `/transition`
- TypeScript: new `OnDone`/`OnFail`/`OnReview`/`OnDoneMerge` enum unions, `DeliverableType`, `Deliverable`, `UNLIMITED` const; `Task` interface expanded with 12 new fields; lifecycle fields narrowed from `string` to enum unions; JSDoc on the three sentinel-using numeric fields
- New `sqlstore.ErrTaskNotFound` sentinel parallel to Project 1's `ErrTagNotFound`
- Spec at `docs/superpowers/specs/2026-04-09-task-api-expansion-design.md` (637 lines, 13 sections)
- Plan at `docs/superpowers/plans/2026-04-09-task-api-expansion.md` (13 tasks, all green)

### Tooling

- Boot prompt for `go-strutil` package: `~/Projects-apps/framework/utils/go-strutil/BOOT.md`
- Spec/plan workflow used: brainstorming → spec → plan → subagent-driven execution → PR → Copilot review → fix commit → merge

## What's Next (Project 3 — Task Detail/Edit Page Rebuild)

The previous TaskDetailPage uses an older visual language (border-border, bg-card, text-xl, generic shadcn) that doesn't match the tasks home page's HUD aesthetic (zinc-950, `text-[10px]` uppercase labels, mono font, accent bars). With Project 2 done, the API now exposes every field the page needs to read and edit.

**Goals (rough — not yet brainstormed):**

1. Rebuild `TaskDetailPage` to match the tasks home visual style
2. Support inline edit via `?edit=1` query param (single page, two modes — option C from Project 1's brainstorming session)
3. Port the **context-aware `NavArrows` component** from Fragments Engine — left/right arrows that navigate among siblings within a scope (project/epic/sprint), auto-advance on delete/archive, preserve scope across sessions. Must work for Tasks, Sprints, Epics, Projects.
4. Use semantic tokens, reusable components, props down / events up / composition (no prop drilling, no over-coupled state)
5. Leverage the now-exposed canonical task fields (tools, deliverables, escalation chain, etc.) to provide a comprehensive edit form

**Reference materials already ported:**

- `docs/architecture/` — design philosophy, unified messaging architecture, MCP registry, special agent patterns, task model v0.1, backlog model, loop runner
- `docs/superpowers/specs/2026-04-07-clockwork-manifold-design.md` — canonical Task record (lines 115-180), dependency resolution rules (530-564)
- `docs/superpowers/plans/2026-04-07-plan-7-gui.md` — original FE GUI plan with task detail layout (2789-3091)

**Approach:** brainstorm scope → spec → plan → subagent-driven execution → PR. Same flow as Projects 1 and 2.

## Approach

- TDD: test first, verify fail, implement, verify pass
- Sequential task execution; bundle small related tasks into one subagent dispatch when they share context
- Two-stage review per task: spec compliance first, then code quality
- Sonnet for implementation tasks, Haiku for reviews and mechanical work
- After backend code changes: rebuild clockwork-api via cerberus, restart clockwork-frontend
- Cerberus manages services: clockwork-api (port 8990), clockwork-frontend (port 5175)
- Stale LSP diagnostics during fast subagent loops are common — verify real state via `go test`/`go build` rather than trusting compiler-error notices in system reminders

## Known follow-ups (deferred, not blocking Project 3)

- **PUT vs PATCH harmonization** — task update uses PUT, tag endpoints use PATCH (pre-existing)
- **Timestamp serialization inconsistency** — `taskJSON` uses raw `time.Time`, `tagJSON` uses `.Format(time.RFC3339)` (cosmetic)
- **N+1 task-tag loading** — `tasksJSON` loads tags per task in a loop; batched query is a clean future cleanup
- **`GOPRIVATE` setup** — `go-strutil` is now consumed from the published `github.com/hollis-labs/go-strutil` repo (no local `replace`). The repo is public on GitHub but not yet indexed by `proxy.golang.org`/`sum.golang.org`, so any future `go get`/`go mod tidy` involving this module needs `GOPRIVATE=github.com/hollis-labs/*` set in the environment (or persisted via `go env -w`). The other hollis-labs modules (`plugin`, `go-queue`) are still on local `replace` directives.
- **`BLG-20260409-001`** — wire `Task.Deliverables` into the scheduler/executor completion gate (API surface is done; the scheduler integration is the next pass)
- **`BLG-20260409-002`** — tag management GUI (post-MVP, P3)
- **Task list endpoint filters on new fields** — not scoped yet; current filter set is status, priority, sprint_id, project_id, epic_id, executor
