# Session Boot — Clockwork Manifold

## Where we left off

Project 3 (Task Detail/Edit Page Rebuild) shipped as PR #3 on 2026-04-11. The three-project chain (Tag system → Task API expansion → Task detail/edit rebuild) is complete: every canonical task field is surfaced through the API and editable via the new HUD-style detail page. `main` is clean at `a5c87ef`, 26 frontend tests passing, cerberus-managed services restarted. Goal for the upcoming work is **getting real task executions runnable so testing can begin.**

## Infrastructure landed but not yet wired

- `sonner <Toaster />` mounted in `apps/gui/src/App.tsx` — ready to consume for action feedback.
- `Task.Deliverables` exposed through the API, but the scheduler does not yet check required deliverables before completing a task.

## Frontend session — wire sonner for action feedback

Priority: turn silent-catch patterns into visible feedback using the already-mounted Toaster.

1. `handleTransition` in `apps/gui/src/pages/TaskDetailPage.tsx` currently has a no-op catch. Replace with `toast.success` on success and `toast.error(err.message)` on failure.
2. Same treatment for `handleAddComment` in `TaskDetailPage.tsx` (unhandled rejection propagates today).
3. Same treatment for `handleTransition` in `apps/gui/src/pages/BoardPage.tsx` (pre-existing silent catch with the same shape).
4. Consider a tiny helper (`useActionToast` or `toastAction` in `@/lib/`) if the call sites justify it; otherwise inline is fine.
5. Verify in browser: force success and failure paths (invalid transition triggers a 4xx).

Approach: brainstorm → spec → plan → subagent-driven → PR → Copilot review → merge.

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
