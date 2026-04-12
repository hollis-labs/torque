# E2E Execution — Path A (Fast Happy Path) — Spec

**Date:** 2026-04-11
**Author:** Backend session (brainstorm)
**Status:** Draft — strict subset of Path B, ready for planning
**Scope:** Smallest unit that proves `create task → scheduler picks → executor runs → task completes` end-to-end with observable state.
**Related:**
- `docs/superpowers/specs/2026-04-11-e2e-execution-prior-art.md`
- `docs/superpowers/specs/2026-04-11-e2e-execution-path-b.md` (full target)
- `.agentrc/boot-prompt.md`

> **Relationship to Path B:** Path A is a strict subset. Every primitive built here is kept as-is in Path B; nothing is rewritten. See Path B's "Path A boundary" section for the invariants.

---

## Goal

A user (or `curl`) can create a task via the API, and a real executor (mock first, CLI second) picks it up, runs it, and transitions it through `todo → doing → done` — all within a single `clockwork serve` process, with the task visible via `GET /api/v1/tasks/{id}`. The deliverables gate works for a simple case (one required artifact, executor produces it, task completes; executor fails to produce it, task blocks).

**Success criteria:**

1. `clockwork serve` boots a single process that runs both the HTTP API and the scheduler. `clockworkd` is unused.
2. `POST /api/v1/tasks` with `{title, description, executor: "mock", status: "todo"}` creates a task.
3. Within one scheduler tick (≤1s), the task transitions to `doing` and a run record is created.
4. Mock executor completes synchronously; lifecycle transitions task per `on_done` rule.
5. `GET /api/v1/tasks/{id}` shows `status: done` after ≤2s wall-clock.
6. `GET /api/v1/runs?task_id={id}` shows the run with `status: done`.
7. The deliverables gate works for a mock task with one required artifact: executor produces it → task completes; executor produces nothing → task blocks.
8. Real CLI executor smoke test: same flow, using a shell-script profile that emits `CLOCKWORK_DONE`. (Covers executor plumbing but no artifact gating beyond Component 5's fix.)
9. `GET /api/v1/scheduler/status` returns real scheduler state; `POST /api/v1/scheduler/toggle` actually toggles.
10. Every integration test suite still passes (`go test ./...`).

**Explicit non-goals (deferred to Path B):**

- Live GUI updates (no SSE bridge for log lines)
- Run event log API (no `GET /api/v1/runs/{id}/events` endpoint)
- GUI task detail page live streaming
- Multiple concurrent tasks with per-run event tailing
- Structured artifact content parsing beyond the minimum needed for the deliverables gate
- Cost ceiling / stale worker UI surface

**Critical Path B invariants preserved (see Path B, "Path A boundary"):**

- `run_events` table IS written during A, even though it's not read by anyone.
- `result.Artifacts` IS populated by executors during A (the plumbing fix is non-optional).
- `WorkerResult.RunID` IS threaded through (bug fix).
- The minimal SSE bridge forwards task-level events so the GUI's existing `/api/v1/events` endpoint shows something.

---

## Step-by-step build order

Each step is independently testable and commitable. Subagent-friendly. Ordered so every step leaves `main` green.

### Step 1 — Fix `WorkerResult.RunID` threading

**Why first:** Bug fix with no external surface. Unblocks every subsequent step that wants to know "which run just completed." Smallest possible change.

**Files:**
- `internal/runtime/scheduler/worker.go` — add `RunID int64` to `WorkerResult`
- `internal/runtime/scheduler/scheduler.go:220-288` — thread captured `runID` through `pool.Submit`; current code uses it in the closure but loses it at result time
- `internal/runtime/scheduler/scheduler.go:294-316` (`DrainResults`) — pass `r.RunID` instead of `0` to `lifecycle.HandleResult`

**Tests:**
- Extend `scheduler/integration_test.go:TestIntegrationFullRoundTrip` to assert `runs[0].ID > 0` and the lifecycle was called with that ID.
- New unit test in `worker_test.go` verifying `WorkerResult.RunID` is populated.

**Exit:** `go test ./internal/runtime/scheduler/...` green.

### Step 2 — Executor artifact plumbing fix

**Why early:** This is the single most impactful gap. Every downstream test and feature depends on it. Leaving it broken until later means retrofitting test fixtures. **Ship in full, not a partial fix.**

**Files:**
- `internal/runtime/executor/types.go` — add `ArtifactEvent(a Artifact) ExecutionEvent` helper
- `plugins/executor-cli/plugin.go:212-215` (print mode) — on `SignalArtifact`, parse payload as JSON `{type, content, url, file_path, metadata}`, append to `result.Artifacts`, emit `ArtifactEvent`
- `plugins/executor-cli/plugin.go:291-314` (stream-JSON mode) — same fix
- `plugins/executor-api/plugin.go` — verify same pattern is applied if it has artifact emission
- `internal/runtime/executor/mock.go` — no change (already populates via `SetResult`)
- `internal/runtime/executor/parser.go` — add `ParseArtifactPayload(payload string) (Artifact, error)` if not present

**Artifact signal shape (documented here as canon):**

```
CLOCKWORK_ARTIFACT {"type":"diff","content":"--- a/x\n+++ b/x\n@@","url":"","file_path":""}
```

Executors emit a JSON blob after the signal keyword. Payload fields: `type` (required), `content` (optional), `url` (optional), `file_path` (optional), `metadata` (optional, object).

**Tests:**
- `executor-cli/plugin_test.go` — new test: signal line with JSON payload → `result.Artifacts` has one entry with correct type/content
- `executor-cli/plugin_test.go` — new test: multiple artifacts in one run, all appear in `result.Artifacts` in emission order
- `executor/parser_test.go` — unit test for `ParseArtifactPayload` with valid/invalid JSON
- `executor-cli/streamparser_test.go` — new case: stream-JSON artifact event populates `result.Artifacts`

**Exit:** `go test ./internal/runtime/executor/... ./plugins/executor-cli/...` green.

### Step 3 — Store methods for `run_events`

**Why now:** The scheduler integration (Step 6) needs these methods to exist. Small, pure DB layer, easy to test in isolation.

**Files:**
- `internal/persistence/sqlstore/run_events.go` (new file)
- `internal/persistence/sqlstore/run_events_test.go` (new file)

**New types:**
```go
type RunEventRecord struct {
    ID        int64
    RunID     sql.NullInt64  // nullable — pre-run task transitions use NULL
    TaskID    string
    Type      string         // "log", "signal", "artifact", "task_transitioned", "run_started", "run_completed", "error"
    Payload   string         // JSON blob
    CreatedAt time.Time
}

type RunEventFilter struct {
    RunID   int64
    TaskID  string
    Types   []string
    SinceID int64
    Limit   int
}
```

**New methods:**
- `AppendRunEvent(evt *RunEventRecord) (int64, error)`
- `ListRunEvents(f RunEventFilter) ([]RunEventRecord, error)` (cursor via `SinceID`, default `Limit` 100)

**Migration note:** `run_events.run_id` is currently `NOT NULL REFERENCES runs(id)` in migration 003. We need it nullable. Add a new migration `006_run_events_nullable_run.sql`:

```sql
-- Make run_id nullable so lifecycle transitions pre-run can be logged
CREATE TABLE run_events_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER REFERENCES runs(id),
    task_id     TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO run_events_new SELECT * FROM run_events;
DROP TABLE run_events;
ALTER TABLE run_events_new RENAME TO run_events;
CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id);
CREATE INDEX IF NOT EXISTS idx_run_events_task ON run_events(task_id);
CREATE INDEX IF NOT EXISTS idx_run_events_type ON run_events(type);
```

**Tests:**
- Append + list roundtrip
- Cursor pagination (insert 5, list since 2, get 3)
- Type filter
- Nullable `run_id` path (insert with NULL, list works)

**Exit:** `go test ./internal/persistence/sqlstore/...` green.

### Step 4 — Scheduler HTTP handlers (replace stubs)

**Why now:** Requires scheduler to be passed into HTTP server. Sets up Step 6's integration with no behavioral changes visible yet (handlers still served by stub until scheduler is wired in).

Actually — this step has to happen *with* Step 6. You can't pass a nil scheduler and return useful data, and you can't build scheduler without touching `serve.go`. **Merge Step 4 into Step 6.**

### Step 5 — Minimal SSE bridge (`SchedulerBridge`)

**Why separately from Step 6:** The bridge is a small, testable unit with no dependency on the scheduler lifecycle. Build and test it in isolation, then wire it in Step 6.

**Files:**
- `internal/httpserver/sse_bridge.go` (new, ~60 lines)
- `internal/httpserver/sse_bridge_test.go` (new)

**Shape:**
```go
type SchedulerBridge struct {
    hub  *SSEHub
    sub  <-chan scheduler.SchedulerEvent
    done chan struct{}
}

func NewSchedulerBridge(hub *SSEHub, bus *scheduler.EventBus) *SchedulerBridge

func (b *SchedulerBridge) Run(ctx context.Context) // blocks until ctx cancelled or bus closes
```

**Behavior in Path A:** Forward **all** events from bus to hub. No filtering. Broadcast as:
```go
hub.Broadcast(evt.Type, map[string]any{
    "task_id": evt.TaskID,
    "run_id":  evt.RunID,
    "payload": evt.Data,
})
```

**Tests:**
- Unit test: construct bus, hub, bridge. Start bridge in goroutine. Publish to bus. Subscribe to hub. Assert event received.
- Shutdown test: cancel context, assert bridge exits and done channel closes.
- Slow subscriber test: hub's drop-on-full behavior is already tested; bridge inherits it.

**Exit:** `go test ./internal/httpserver/...` green.

### Step 6 — Scheduler integration into `clockwork serve` (the big one)

**Why last-but-foundation:** This is the architectural change. Every prior step was prep. Once this lands, the system is end-to-end wired and Step 7 (smoke tests) validates it.

**Files:**
- `cmd/clockwork/serve.go` — substantial rewrite of the run path
- `internal/httpserver/server.go` — add scheduler reference, update `New()` signature
- `internal/httpserver/settings.go:59-75` — replace stub `schedulerStatus` / `schedulerToggle` with real scheduler calls
- `internal/runtime/scheduler/scheduler.go:220-288` — in the event callback, after `s.bus.Publish`, also call `s.store.AppendRunEvent` for each event type (see event → record mapping in Path B Component 4)
- `internal/runtime/scheduler/lifecycle.go:164-185` — in `transition()`, after `TransitionTask`, call `AppendRunEvent` with `type=task_transitioned` and `run_id=NULL` (pre-run) or the captured run ID (post-run)

**New `serve.go` shape (pseudocode):**

```go
func serveCmd() *cobra.Command {
    return &cobra.Command{
        Use: "serve",
        RunE: func(cmd *cobra.Command, args []string) error {
            cfg, _ := config.Load()
            db, driver, _ := appdb.Open()
            defer db.Close()
            migrations.Run(db)
            store, _ := sqlstore.New(db, driver)

            // NEW: queue
            queuePath := filepath.Join(cfg.DataDir, "queue.db")
            os.MkdirAll(cfg.DataDir, 0755)
            q, _ := queue.Open(queuePath)
            defer q.Close()

            // NEW: executor registry + bootstrap
            registry := executor.NewRegistry()
            registry.Register(executor.NewMockExecutor())
            bootstrap.Executors(registry, cfg.Profiles, nil)

            // NEW: scheduler
            sched := scheduler.New(store, q, registry, &cfg.Scheduler)

            // existing service, updated New() with sched
            svc := service.New(store)
            handler := httpserver.New(svc, sched)

            // NEW: SSE bridge
            bridge := httpserver.NewSchedulerBridge(handler.SSEHub(), sched.EventBus())

            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()

            var wg sync.WaitGroup
            wg.Add(2)
            go func() { defer wg.Done(); bridge.Run(ctx) }()
            go func() { defer wg.Done(); sched.Run(ctx) }()

            srv := &http.Server{Addr: addr, Handler: handler}
            go handleSignals(cancel, srv)

            if err := srv.ListenAndServe(); err != http.ErrServerClosed {
                cancel()
                wg.Wait()
                return err
            }
            cancel()
            wg.Wait()
            return nil
        },
    }
}
```

**Interface change to `httpserver.New`:**
```go
// internal/httpserver/server.go
func New(svc *service.Service, sched *scheduler.Scheduler) http.Handler {
    s := &Server{
        svc:    svc,
        sched:  sched,  // new field
        router: chi.NewRouter(),
        sse:    NewSSEHub(),
    }
    s.routes()
    return s
}

// Expose SSE hub so serve.go can pass to bridge
func (s *Server) SSEHub() *SSEHub { return s.sse }
```

**Nil-safety for existing tests:** Every `httpserver.New(svc, nil)` call must still work. Handlers that need sched (`schedulerStatus`, `schedulerToggle`) return 503 if `s.sched == nil`. This keeps existing test files green without forcing them all to construct a scheduler.

**Replace stubs (`settings.go:59-75`):**
```go
func (s *Server) schedulerStatus(w http.ResponseWriter, r *http.Request) {
    if s.sched == nil {
        writeError(w, http.StatusServiceUnavailable, "scheduler not running")
        return
    }
    writeJSON(w, http.StatusOK, s.sched.Status())
}

func (s *Server) schedulerToggle(w http.ResponseWriter, r *http.Request) {
    if s.sched == nil {
        writeError(w, http.StatusServiceUnavailable, "scheduler not running")
        return
    }
    status := s.sched.Status()
    s.sched.SetEnabled(!status.Enabled)
    writeJSON(w, http.StatusOK, s.sched.Status())
}
```

**Scheduler event callback update (`scheduler.go:224-242`):**
```go
cb := func(event executor.ExecutionEvent) {
    s.heartbeat.Beat(capturedWorkerID)

    // Existing: persist artifacts
    if event.Type == executor.EventArtifact && event.Artifact != nil {
        s.store.CreateArtifact(&sqlstore.ArtifactRecord{...})
    }

    // NEW: persist run event
    payload, _ := json.Marshal(event)
    s.store.AppendRunEvent(&sqlstore.RunEventRecord{
        RunID:   sql.NullInt64{Int64: capturedRunID, Valid: true},
        TaskID:  capturedTaskID,
        Type:    event.Type.String(),  // "log", "signal", "artifact", etc.
        Payload: string(payload),
    })

    // Existing: broadcast
    s.bus.Publish(SchedulerEvent{...})
}
```

**Tests:**
- `cmd/clockwork/serve_test.go` — probably doesn't exist today; skip if too expensive to add. Covered by integration tests instead.
- `httpserver/settings_test.go` — add test for scheduler status/toggle with real scheduler
- `scheduler/integration_test.go:TestIntegrationFullRoundTrip` — extend to assert `run_events` rows exist for the completed run
- **New:** `cmd/clockwork/serve_e2e_test.go` — spins up `serve` in a goroutine, hits `POST /api/v1/tasks`, polls `GET /api/v1/tasks/{id}` until `done`, asserts the transition happened. This is the true smoke test.

**Exit:** `go test ./...` green. Manual smoke (Step 7) next.

### Step 7 — Deliverables gate verification with mock executor

**Why:** Closes out `BLG-20260409-001` for the mock case. Real CLI case goes to Path B.

**Files:**
- `internal/runtime/scheduler/deliverables_e2e_test.go` (new)

**Cases:**
1. Task with `deliverables=[{type: diff, required: true}]`, mock produces `{type: diff, content: "..."}` → task `done`.
2. Same task, mock produces no artifacts → task `blocked` with `blocked_reason` containing "missing required deliverables".
3. Two required deliverables, mock produces one → `blocked`.
4. One required + one optional deliverable, mock produces required only → `done`.

**Exit:** `go test ./internal/runtime/scheduler/...` green. `BLG-20260409-001` closable for mock path.

### Step 8 — Real CLI executor smoke

**Why:** Validates Step 2's artifact plumbing fix against a non-mock executor.

**Files:**
- `docs/examples/profiles/smoke-echo.yaml` (new) — minimal profile definition
- `docs/examples/profiles/smoke-echo.sh` (new) — shell script that echoes `CLOCKWORK_DONE` (and optionally a `CLOCKWORK_ARTIFACT` signal)
- `cmd/clockwork/smoke_cli_test.go` (new) — spins up `serve`, creates a task pointing at this profile, polls to completion

**Script shape:**
```bash
#!/bin/bash
# smoke-echo.sh — minimal executor-cli smoke profile
echo "starting smoke run"
echo 'CLOCKWORK_ARTIFACT {"type":"log","content":"smoke run complete"}'
echo "CLOCKWORK_DONE"
```

**Profile shape:**
```yaml
# smoke-echo.yaml
smoke-echo:
  command: docs/examples/profiles/smoke-echo.sh
  args: []
  timeout_seconds: 10
```

**Test:**
- Task with `executor: "cli"`, `agent_profile: "smoke-echo"`
- Verify task transitions to `done`
- Verify `result.Artifacts` has one entry of type `log`
- Verify `run_events` has entries for log + artifact + done signal

**Exit:** Smoke test green. Path A complete.

---

## Component inventory (cross-reference Path B)

| Component (Path B) | Path A? | Notes |
|---|---|---|
| 1. Scheduler in `serve` | Yes, full | Step 6 |
| 2. Scheduler HTTP handlers | Yes, full | Step 6 (folded into serve change) |
| 3. Store `run_events` methods | Yes, full | Step 3 |
| 4. Scheduler writes run events | Yes, full | Step 6 (callback update) |
| 5. Executor artifact plumbing | **Yes, full** | Step 2 — non-negotiable |
| 6. `RunID` in `WorkerResult` | Yes, full | Step 1 |
| 7. SSE bridge | Yes, minimal | Step 5 — forwards all events without filtering; Path B keeps same shape |
| 8. Run events HTTP API | **No** | Deferred |
| 9. Deliverables E2E tests | Mock only | Step 7; real CLI deferred to Path B |
| 10. Task-scoped SSE filter | No | Deferred |
| 11. GUI wiring | No | Separate frontend session |

---

## Tests to add/extend (full list)

- `internal/runtime/scheduler/worker_test.go` — `WorkerResult.RunID` populated
- `internal/runtime/scheduler/integration_test.go` — `TestIntegrationFullRoundTrip` asserts run events + run ID
- `internal/runtime/scheduler/deliverables_e2e_test.go` — NEW, 4 mock cases
- `internal/runtime/executor/parser_test.go` — `ParseArtifactPayload` unit tests
- `plugins/executor-cli/plugin_test.go` — artifact signals populate `result.Artifacts` (print mode + stream-JSON mode)
- `plugins/executor-api/plugin_test.go` — same verification if applicable
- `internal/persistence/sqlstore/run_events_test.go` — NEW, append/list/cursor/nullable RunID
- `internal/persistence/sqlstore/migrations/migrate_test.go` — verify new migration `006_run_events_nullable_run.sql` applies cleanly over existing schema
- `internal/httpserver/sse_bridge_test.go` — NEW, bridge forwards events; shutdown clean
- `internal/httpserver/settings_test.go` — scheduler status/toggle with real + nil scheduler
- `cmd/clockwork/serve_e2e_test.go` — NEW, HTTP POST + poll to completion
- `cmd/clockwork/smoke_cli_test.go` — NEW, real CLI executor smoke

**Expected test count delta:** +~20 tests (from 478 baseline). Full suite must stay green.

---

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| `result.Artifacts` plumbing fix breaks existing executor tests | Add the append alongside the existing `cb` call, not instead of. Existing tests that only check `cb` output still pass. |
| Nil-safe scheduler in `httpserver.New` causes test drift | 503 responses are explicit and tested. Existing tests that construct `httpserver.New(svc, nil)` never hit scheduler endpoints anyway. |
| Migration `006` fails on existing dev databases | Migration is a rebuild-then-rename pattern; test it over a populated DB in `migrate_test.go`. |
| Scheduler goroutine leaks on shutdown | `scheduler.Stop(ctx)` already drains. `sync.WaitGroup` in `serve.go` ensures bridge + scheduler both exit before `Close()`. |
| Queue.db file locked if both `clockworkd` and `serve` run | **Action:** Document that `clockworkd` is deprecated; remove it in the same PR as Step 6. Prevents accidental concurrent boot. |
| Cerberus currently manages `clockwork-api` and `clockwork-frontend` but no `clockworkd` | No action — Cerberus already doesn't run `clockworkd`. `clockwork serve` is what it launches. Good luck. |
| SQLite WAL contention from run event writes | Monitor in Step 7 smoke test; batch later if needed. Write volume is a handful per run, not thousands. |
| `bootstrap.Executors(registry, cfg.Profiles, nil)` — the `nil` is the tool router, which doesn't exist yet | Matches current intent from `bootstrap.go:13` comment. Keep the `interface{}` parameter; pass nil. |

---

## Assumptions that need verifying during execution

1. **`cfg.Profiles` exists on the config.** Grep `config.Load()` output. If it's called something else, adjust Step 6.
2. **`bootstrap.Executors` signature still accepts `(registry, profiles, toolRouter)`.** Verified in prior reading; re-verify before Step 6.
3. **`runs.id` auto-increment is stable across integration test teardowns.** Verified — it's `INTEGER PRIMARY KEY AUTOINCREMENT`.
4. **`migrations.Run()` picks up a new `006_*.sql` file automatically.** Check `migrate.go` logic — if it's a hardcoded list, add to the list in the same commit.

---

## Delete list (once Path A lands)

- `cmd/clockworkd/main.go` — redundant after Step 6. Scheduler lives in `serve`; `clockworkd` is dead code.
- Any Cerberus / Makefile targets referencing `clockworkd` — check and update.
- Stub response strings in `internal/httpserver/settings.go` ("Scheduler not yet implemented — see Plan 2")

**Confirm before deleting:** Is there any external expectation (docs, tests, deploy scripts) that `clockworkd` is a separate binary? If yes, deprecate first, delete in Path B.

---

## Exit criteria

Path A is done when:

1. Every test in the "Tests to add/extend" list is green.
2. `go test ./...` fully green.
3. Manual smoke: `clockwork serve` booted, `curl POST /api/v1/tasks` with mock executor, poll `GET /api/v1/tasks/{id}` until `done`, observe the transition in ≤2s.
4. Manual smoke: same with `executor: "cli"` and the `smoke-echo` profile — task transitions to `done` and `result.Artifacts` has the expected log entry in the DB.
5. Deliverables gate mock tests pass.
6. `BLG-20260409-001` noted as partially closed (mock path verified, real CLI path deferred to Path B).
7. Updated boot prompt with Path A status and Path B next steps.
8. PR opened, Copilot review round, merged.

---

## Out of scope (explicit)

- GUI live updates
- Run events HTTP API
- Real CLI deliverables E2E (beyond the smoke test, which just verifies plumbing)
- Cost / stale worker UI surfacing
- Any frontend changes
- Observability polish beyond what falls out of Steps 1-8
- Backend follow-ups from boot prompt item 4 (PUT vs PATCH, timestamps, N+1, task filters) — all deferred

---

## Handoff to planning session

Use `sp-writing-plans` on this spec to produce the step-by-step implementation plan. Steps 1-8 are already ordered and scoped for subagent-driven execution. Each step has files, test targets, and an exit criterion. The plan should mostly be: "For each step, TDD, verify, commit."

**Suggested subagent dispatch (Step 6 is the exception — single-session, not subagent, because of the architecture change scope):**

- Step 1 → subagent
- Step 2 → subagent (multi-file but single concept)
- Step 3 → subagent
- Step 5 → subagent
- Step 6 → main session, supervised
- Step 7 → subagent
- Step 8 → subagent

Step 4 is folded into Step 6. Step 5 can technically run in parallel with Step 3 (independent files). Steps 1 and 2 are independent and could also run in parallel. Steps 3-5 must complete before Step 6.

**Branch:** `feat/e2e-execution` (already created as worktree at `../clockwork-manifold-backend-e2e`).
