# Session Boot — Clockwork Manifold

## Where we left off

Project 3 (Task Detail/Edit Page Rebuild) shipped as PR #3 on 2026-04-11. The three-project chain (Tag system → Task API expansion → Task detail/edit rebuild) is complete: every canonical task field is surfaced through the API and editable via the new HUD-style detail page.

Frontend session on 2026-04-11 produced an **approved spec for the Sonner toast migration** (see `docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md`, tracked as `BLG-20260411-001`). Implementation was deferred to a future frontend session so the current session could close out and hand control to the backend agent. The design extends beyond the original 3 sites: 16 silent-catch mutation handlers across 9 pages, routed through a new `@/lib/toast` module (single source of truth, errors-only this pass, `notifySuccess` exported but unused).

Goal for upcoming backend work is **getting real task executions runnable end-to-end so iterative testing can begin.**

## Infrastructure landed but not yet wired

- `sonner <Toaster />` mounted in `apps/gui/src/App.tsx` — consumer design approved, not yet implemented (`BLG-20260411-001`).
- `Task.Deliverables` exposed through the API, but the scheduler does not yet check required deliverables before completing a task.

## Frontend session — execute approved sonner toast migration

Spec is approved and committed. Next frontend session resumes from:

1. Invoke `superpowers:writing-plans` against `docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md` to produce the implementation plan.
2. Execute subagent-driven: create `apps/gui/src/lib/toast.ts` + its unit tests first, then migrate the 16 call sites (list and fallback messages in the spec), then add 2 smoke tests.
3. Verify in browser per spec — force valid transition (no toast expected) and invalid transition (red toast with API message).
4. PR → Copilot review → merge.

Do NOT re-brainstorm. The design is approved. The spec captures all scope decisions (errors-only, EmptyState untouched, save banner untouched, best-effort prefetches untouched, no hook yet).

## Backend session — executions runnable end-to-end

Priority: close the gap between "create a task" and "watch it execute and complete" so the user can start iterating on real runs.

1. **Deliverables completion gate** (`BLG-20260409-001`). Wire `Task.Deliverables` into `internal/runtime/` so a task with `deliverables: [{type: diff, required: true}]` cannot transition to `done` until the required artifact exists. Honour `on_done` rules.
2. **End-to-end smoke run.** Pick a simple task spec (echo, file-write, whatever). Run it through the daemon: `queued → doing → review/done`. Document every gap that surfaces — missing SSE events, executor stdout capture, error propagation, permission mode interactions. Fix the blocking ones.
3. **Observability pass.** Whatever makes it feel good to iterate on executions — better run events, executor output capture, clearer error messages. Scope narrowly: only what's blocking testing.
4. **Backend follow-ups** (in priority order, as time allows):
   - PUT vs PATCH harmonization (task update uses PUT, tag endpoints use PATCH — pick one)
   - `taskJSON` timestamp serialization (raw `time.Time` vs `.Format(time.RFC3339)` in `tagJSON`)
   - N+1 task-tag loading in `tasksJSON` (batch the query)
   - Task list endpoint filters on the new Project-2 fields (currently only status, priority, sprint_id, project_id, epic_id, executor)
   - **Transition state machine — allow `* → backlog` from any open status.** Surfaced 2026-04-11 during sonner-toast verification: attempting `todo → backlog` from the GUI hits `cannot transition from todo to backlog: transition not permitted`. Parking any task back to the backlog is a standard workflow move and should be permitted from any non-terminal status. Check the transition rule table in `internal/service/task.go` (or wherever the transition matrix lives) and widen the allowed sources for the `backlog` target. Keep `done`/archived exits intact.
5. Run `go test ./...` after every change. Project has 478+ backend tests; keep them green.

Files to explore first: `internal/runtime/` (scheduler, executor), `internal/service/task.go`, `internal/persistence/sqlstore/`, `internal/httpserver/`, `plugins/executor-*/`.

Approach: same as frontend — brainstorm → spec → plan → subagent-driven → PR → Copilot review → merge. TDD for Go work.

## Backend session — executions runnable end-to-end

Priority: close the gap between "create a task" and "watch it execute and complete" so the user can start iterating on real runs.

1. **Deliverables completion gate** (`BLG-20260409-001`). Wire `Task.Deliverables` into `internal/runtime/` so a task with `deliverables: [{type: diff, required: true}]` cannot transition to `done` until the required artifact exists. Honour `on_done` rules.
2. **End-to-end smoke run.** Pick a simple task spec (echo, file-write, whatever). Run it through the daemon: `queued → doing → review/done`. Document every gap that surfaces — missing SSE events, executor stdout capture, error propagation, permission mode interactions. Fix the blocking ones.
3. **Observability pass.** Whatever makes it feel good to iterate on executions — better run events, executor output capture, clearer error messages. Scope narrowly: only what's blocking testing.
4. **Backend follow-ups** (in priority order, as time allows):
   - PUT vs PATCH harmonization (task update uses PUT, tag endpoints use PATCH — pick one)
   - `taskJSON` timestamp serialization (raw `time.Time` vs `.Format(time.RFC3339)` in `tagJSON`)
   - N+1 task-tag loading in `tasksJSON` (batch the query)
   - Task list endpoint filters on the new Project-2 fields (currently only status, priority, sprint_id, project_id, epic_id, executor)
5. Run `go test ./...` after every change. Project has 478+ backend tests; keep them green.

Files to explore first: `internal/runtime/` (scheduler, executor), `internal/service/task.go`, `internal/persistence/sqlstore/`, `internal/httpserver/`, `plugins/executor-*/`.

Approach: same as frontend — brainstorm → spec → plan → subagent-driven → PR → Copilot review → merge. TDD for Go work.

## Deferred (not in scope for these sessions)

NavArrows + context-aware back-links (wait until all 4 detail pages are on HUD); Tier 5 field editors (permissions/metadata) need a real design pass; other detail page HUD rebuilds; inline field-level edit conveniences; SSE live updates on detail page; `@mention` task ID picker; tag management GUI (`BLG-20260409-002`); duplication extraction from Tasks 5–8 section helpers.

## Environment notes

- `GOPRIVATE=github.com/hollis-labs/*` is already set in this dev env (required for `go-strutil`, `plugin`, `go-queue` modules not yet indexed by the Go proxy).
- Cerberus manages `clockwork-api` (8990) and `clockwork-frontend` (5175). After backend changes, rebuild via cerberus and restart. Frontend needs a service restart to pick up merged changes.
- Stale LSP diagnostics during fast subagent loops are common — verify real state via `go test` / `npm run build` / `npm run test:run` rather than trusting compiler-error system reminders.
