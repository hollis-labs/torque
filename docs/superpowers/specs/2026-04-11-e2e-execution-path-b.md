# E2E Execution — Path B (Iteration-Ready Loop) — Spec

**Date:** 2026-04-11
**Author:** Backend session (brainstorm)
**Status:** Draft — full target design. Path A is a strict subset.
**Scope:** The full "iteration-ready" shape of E2E task execution.
**Related:**
- `docs/superpowers/specs/2026-04-11-e2e-execution-prior-art.md`
- `docs/superpowers/specs/2026-04-11-e2e-execution-path-a.md` (strict subset)
- `.agentrc/boot-prompt.md` — backend session goals

> **Purpose of this doc:** Lock down the full target so Path A can be built as a strict subset without painting itself into a corner. Every primitive listed here should either be built in Path A (with minimal surface) or have a clean seam in Path A that Path B extends. **No Path B primitive should require rewriting a Path A primitive.**

---

## Goal

A user can create a task in the GUI and **watch it execute live** from the task detail page. Multiple tasks can queue and run concurrently. The deliverables gate is verified end-to-end with a real executor producing artifacts. Error paths (executor failure, missing deliverable, timeout, cost ceiling) are visible and debuggable from the GUI without tailing logs.

**Success criteria:**

1. `POST /api/v1/tasks` creates a task with `status=todo`.
2. Scheduler picks it within ≤1 tick, transitions to `doing`, creates a run record.
3. Executor streams log lines, signals, and artifacts during execution.
4. Each stream event is persisted to `run_events` and broadcast over SSE.
5. GUI's task detail page subscribes to a task-scoped SSE stream and renders:
   - Current status (live)
   - Run list (polling or SSE)
   - Selected run's event log (live tail, paginated history)
   - Artifacts as they arrive
6. On task completion:
   - If `deliverables` are declared and the executor produced artifacts matching each required type, task transitions per `on_done` rule.
   - If a required deliverable is missing, task transitions to `blocked` with a clear `blocked_reason`.
7. On executor failure, task transitions per `on_fail` rule; failure reason visible in GUI.
8. Scheduler toggle (`POST /api/v1/scheduler/toggle`) pauses / resumes dispatch. Status endpoint returns real data.
9. Multiple concurrent tasks run correctly under the worker pool. Cost ceiling and stale-worker detection both verified in integration tests.
10. Both the mock executor and the real CLI executor pass the E2E smoke path.

**Non-goals (still deferred even in Path B):**

- GUI for cancelling runs mid-execution (primitives in place; UI is a follow-up)
- Task priority rebalancing UI
- Cost budget UI per sprint
- Multi-node scheduler (single-process only)
- Workflow/pipeline tasks (single task, single run only — no DAG)

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     clockwork serve (single process)            │
│                                                                 │
│  ┌─────────────┐   ┌──────────────┐   ┌─────────────────────┐   │
│  │ HTTP/API    │──▶│ Service      │──▶│ SQLite (tasks, runs,│   │
│  │ + SSEHub    │   │ (tasks,      │   │ run_events,         │   │
│  │             │   │  scheduler)  │   │ artifacts)          │   │
│  └──────┬──────┘   └──────┬───────┘   └─────────────────────┘   │
│         │                 │                     ▲               │
│         │ bridge          │                     │               │
│         │ (goroutine)     ▼                     │               │
│         │          ┌─────────────┐              │               │
│         └──────────│ Scheduler   │──────────────┘               │
│         EventBus   │ - Picker    │    CreateRun                 │
│                    │ - Worker    │    AppendRunEvent            │
│                    │   Pool      │    CreateArtifact            │
│                    │ - Lifecycle │    TransitionTask            │
│                    │ - Deliverab │                              │
│                    │   Gate      │                              │
│                    └──────┬──────┘                              │
│                           │                                     │
│                           ▼                                     │
│                    ┌─────────────┐                              │
│                    │ Executor    │                              │
│                    │ Registry:   │                              │
│                    │ - mock      │                              │
│                    │ - cli       │                              │
│                    │ - api       │                              │
│                    └─────────────┘                              │
└─────────────────────────────────────────────────────────────────┘
```

**Key flow changes from today:**

1. Scheduler lives inside `clockwork serve` (Option A decision).
2. `bootstrap.Executors()` registers `cli` + `api` executors. Mock stays registered for testing.
3. A single adapter goroutine subscribes to `scheduler.EventBus` and republishes to `httpserver.SSEHub` as typed events.
4. Executor event callbacks, in addition to publishing to the bus and creating artifacts, also **write to `run_events`** via a new `Store.AppendRunEvent()` method.
5. Executor plugins **populate `result.Artifacts`** as they stream (not just via callback), so the deliverables gate sees them.
6. The run record is updated incrementally (status, heartbeats) via new `Store.UpdateRun()` calls, not just on completion.

---

## Components (by file)

### Component 1 — Scheduler integration into `clockwork serve`

**File:** `cmd/clockwork/serve.go` (~30 line diff)

**Changes:**
- Build scheduler with `scheduler.New(store, queue, registry, &cfg.Scheduler)`.
- Call `bootstrap.Executors(registry, cfg.Profiles, nil)` before `scheduler.New`.
- Run scheduler as a goroutine alongside HTTP server.
- On SIGINT, cancel scheduler context, wait for it to drain, then close HTTP.
- Pass scheduler reference into `httpserver.New(svc, sched)`.

**Interface change:**
```go
// Before
func New(svc *service.Service) http.Handler

// After
func New(svc *service.Service, sched *scheduler.Scheduler) http.Handler
```

**Why:** Single-process architecture (D5 from prior art). Eliminates the dual-DB-handle race that the two-process model has today and gives HTTP handlers a real scheduler reference.

**Queue path:** The old `clockworkd` opened a separate `queue.db` at `cfg.DataDir/queue.db`. Path B keeps the same path for compatibility with existing tests. `clockwork serve` takes ownership of queue lifecycle.

**Old `clockworkd` binary:** **Deleted in Path B.** The executable becomes dead code once `serve` hosts the scheduler. Path A keeps it for parallel-running confidence during the migration; Path B removes it.

### Component 2 — Real scheduler HTTP handlers

**File:** `internal/httpserver/settings.go` (replaces stubs at lines 59-75)

**Endpoints:**

- `GET /api/v1/scheduler/status` → `sched.Status()` (already returns the right shape)
- `POST /api/v1/scheduler/toggle` → `sched.SetEnabled(!sched.Status().Enabled)`; return new status
- `POST /api/v1/scheduler/tick` (new, optional) → `sched.Tick(ctx)` for manual step-through debugging

**Why:** GUI scheduler controls actually work. Currently stub responses hide scheduler state.

### Component 3 — Store support for `run_events`

**File:** `internal/persistence/sqlstore/runs.go` (new methods, ~80 lines)

**New types:**
```go
type RunEventRecord struct {
    ID        int64
    RunID     int64
    TaskID    string
    Type      string     // "log", "signal", "artifact", "transition", "error"
    Payload   string     // JSON or raw content
    CreatedAt time.Time
}

type RunEventFilter struct {
    RunID     int64
    TaskID    string
    Types     []string
    SinceID   int64      // cursor for pagination / tailing
    Limit     int
}
```

**New methods:**
- `AppendRunEvent(evt *RunEventRecord) (int64, error)` — single-row insert
- `ListRunEvents(f RunEventFilter) ([]RunEventRecord, error)` — cursor-paginated
- `UpdateRunStatus(id int64, status string) error` — for intermediate state updates

**Why:** The `run_events` table already exists from migration `003_concurrency.sql:3`. It has no Go wrapper. Hadron and Fragments-Engine both show this append-only event log pattern; it's the single most valuable primitive for "watch a run live."

**Cursor semantics:** Use auto-increment `id` as the cursor. `SinceID > 0` returns events with `id > SinceID` ordered ASC. Empty `SinceID` returns the full tail (with `Limit` applied). Simpler than time-range cursors and monotonically correct.

### Component 4 — Scheduler writes run events

**File:** `internal/runtime/scheduler/scheduler.go:224-242` (event callback inside `dispatchTask`)

**Changes:** In the `cb` closure, after publishing to `bus`, also call `s.store.AppendRunEvent`. Map each `ExecutionEvent` type to a `RunEventRecord` type:

| ExecutionEvent        | RunEventRecord.Type  | Payload                          |
|-----------------------|----------------------|----------------------------------|
| `EventLog`            | `log`                | `{"line": "..."}`                |
| `EventSignal`         | `signal`             | `{"signal": "...", "payload": ""}` |
| `EventArtifact`       | `artifact`           | `{"type": "...", "content": ""}` |
| `EventTokenUsage`     | `tokens`             | `{"prompt": N, "completion": N, "cost": N}` |
| `EventProgress`       | `progress`           | `{"value": 0.5}`                 |

**Additionally:** At dispatch time, write a `run_started` event. At lifecycle transition, write `task_transitioned` (either here or in lifecycle manager — see D7 below).

**Why:** Event persistence is the primitive that enables async observability (reconnect after disconnect), audit trails, and GUI log viewers that don't need to stream.

### Component 5 — Executor artifact plumbing fix

**Files:**
- `plugins/executor-cli/plugin.go:212-215` (currently only emits via `cb`)
- `plugins/executor-cli/plugin.go:291-314` (stream-JSON path has the same gap)
- `plugins/executor-api/plugin.go` (verify — probably same gap)
- `internal/runtime/executor/mock.go` (already populates `result.Artifacts` via `SetResult`)

**Change pattern (per executor):**
```go
// Before: only emits via cb
cb(executor.SignalEvent(sig.Type.String(), sig.Payload))

// After: also appends to result.Artifacts for artifact signals
if sig.Type == executor.SignalArtifact {
    art := executor.Artifact{
        Type:    parsed.Type,
        Content: parsed.Content,
        // ...
    }
    result.Artifacts = append(result.Artifacts, art)
}
cb(executor.ArtifactEvent(art))  // structured event, not raw signal
```

**New helper:** `executor.ArtifactEvent(a Artifact) ExecutionEvent` — currently only `SignalEvent` / `LogEvent` / `TokenEvent` exist.

**Why:** The deliverables gate at `lifecycle.go:54-66` checks `result.Artifacts` — it will never see streamed artifacts unless they're also appended to the result struct. Today's code passes the deliverables gate only in tests where `MockExecutor.SetResult` pre-populates `Artifacts`. Real executors silently fail the gate.

**Parsing note:** The current CLI executor emits artifact signals as `SignalArtifact` with raw payload; there's no structured parser. Path B adds a small JSON parser for the artifact signal payload (type + content + metadata). Path A can emit minimal shape (just `type`) to satisfy the gate.

### Component 6 — `DrainResults` run ID fix

**File:** `internal/runtime/scheduler/scheduler.go:294-316`

**Bug:** `DrainResults` calls `lifecycle.HandleResult(r.TaskID, 0, r.Result)` with hardcoded `runID=0`. Lifecycle never knows which run completed. This means:
- Run event writes from lifecycle can't reference the run
- Deliverables gate retries don't know which run to blame
- Any per-run metrics are broken

**Fix:** `WorkerResult` needs to carry `RunID`. `Pool.Submit` needs to accept it. Dispatch threads the captured `runID` through.

```go
// internal/runtime/scheduler/worker.go
type WorkerResult struct {
    TaskID string
    RunID  int64  // NEW
    Result *executor.ExecutionResult
    Err    error
}
```

**Why:** Silently broken today. Must fix before any run-scoped feature.

### Component 7 — EventBus → SSEHub bridge

**File:** `internal/httpserver/sse_bridge.go` (new, ~60 lines)

```go
type SchedulerBridge struct {
    hub *SSEHub
    sub <-chan scheduler.SchedulerEvent
    done chan struct{}
}

func NewSchedulerBridge(hub *SSEHub, bus *scheduler.EventBus) *SchedulerBridge {
    return &SchedulerBridge{
        hub:  hub,
        sub:  bus.Subscribe(),
        done: make(chan struct{}),
    }
}

func (b *SchedulerBridge) Run(ctx context.Context) {
    defer close(b.done)
    for {
        select {
        case <-ctx.Done():
            return
        case evt, ok := <-b.sub:
            if !ok {
                return
            }
            data := map[string]interface{}{
                "task_id": evt.TaskID,
                "run_id":  evt.RunID,
                "payload": evt.Data,
            }
            b.hub.Broadcast(evt.Type, data)
        }
    }
}
```

**Wired into `serve.go`:** After building scheduler and server, launch bridge as goroutine.

**Why:** `EventBus` and `SSEHub` are both non-blocking fan-out buses. A single adapter closes the gap. This is the pattern Nanite uses verbatim.

**Filter decision (D6):** Bridge forwards **all** scheduler events to the hub. Client-side filtering happens in the browser. Server-side per-task SSE endpoints (`/api/v1/tasks/{id}/events`) are an optional extension in Path B — see "Optional extensions" below.

### Component 8 — Run events HTTP API

**Files:**
- `internal/httpserver/runs.go` (new endpoints)
- `internal/service/runs.go` (new service methods)

**New endpoints:**
- `GET /api/v1/runs/{id}/events?since_id=N&limit=M` — paginated tail
- `GET /api/v1/tasks/{id}/runs` — already exists via `listRuns` but with task filter
- `GET /api/v1/tasks/{id}/events?since_id=N` — task-scoped event feed (optional, see D6)

**Why:** GUI task detail page needs to fetch event history when the user navigates to a task mid-run or after it finished. Live SSE handles the tail; this handles the head.

### Component 9 — Deliverables gate full-path verification

**Files:**
- `internal/runtime/scheduler/lifecycle.go:53-87` (already half-wired)
- **New integration test:** `internal/runtime/scheduler/deliverables_e2e_test.go`

**Test cases:**
1. Task with `deliverables: [{type: diff, required: true}]`, mock executor produces artifact with `type=diff` → task transitions `done`.
2. Same task, mock executor produces no artifacts → task transitions `blocked` with `blocked_reason` matching.
3. Task with two required deliverables, executor produces one → `blocked`.
4. Task with one required + one optional deliverable, executor produces only required → `done`.
5. Same full roundtrip against real CLI executor with a profile that emits `CLOCKWORK_ARTIFACT` signals.

**Why:** Boot prompt asks specifically for this. Half-wired today — the check exists but artifact plumbing bug means it's untested with real streaming.

### Component 10 — Task-scoped SSE filtering (optional D6 extension)

**Decision point — keep or defer?** GUI task detail page needs to subscribe to events *for one specific task*, not the firehose. Two shapes:

- **B.1 — Client-side filter.** Subscribe to `/api/v1/events`, filter by `task_id` in the browser. Dead simple. Wastes a bit of network, but "a bit" is trivial at this scale.
- **B.2 — Server-side filter.** New endpoint `/api/v1/tasks/{id}/events` that creates a filtered subscription. More complex.

**Recommended:** B.1 in Path B v1, B.2 as a future optimization if network cost matters. Don't build what's not needed.

### Component 11 — GUI wiring (parallel session, not in this session's scope)

Path B requires the GUI task detail page to:
- Subscribe to `/api/v1/events` on mount and update task status live on `task.transitioned` events
- Render the `run` list for the task (poll once, refresh on `run.started` / `run.completed`)
- When a run is selected, fetch `/api/v1/runs/{id}/events?since_id=0&limit=100` and then live-tail via the SSE bus
- Render artifacts as they arrive

**This work is deferred to a frontend session.** Path B backend provides the primitives; the GUI session implements the surface.

---

## Named decisions

**D1 — Single-process architecture.** Scheduler runs inside `clockwork serve`. `clockworkd` becomes dead code in Path B and is deleted.

**D2 — `run_events` is the source of truth for run observability.** Written by the scheduler on every executor event. Read by HTTP for history + live tail.

**D3 — Both streaming and append artifacts in executors.** Artifacts emitted via `cb` AND appended to `result.Artifacts`. Deliverables gate reads the result struct; run events log comes from the callback. Two consumers, two code paths, one source (the executor).

**D4 — `RunID` threads through `WorkerResult`.** Fix the hardcoded `0` at dispatch time, not at lifecycle time.

**D5 — Bridge goroutine is the only `EventBus → SSEHub` path.** No ad-hoc publishes from HTTP handlers. One seam, one owner.

**D6 — Client-side task filtering for SSE.** Broadcast all events on the firehose; filter in the browser. Per-task endpoints deferred until network cost justifies them.

**D7 — Lifecycle manager owns `task_transitioned` run events.** Scheduler owns `run_started` / `log` / `artifact` / `run_completed`. Lifecycle owns the transition log. Clean division of labor.

**D8 — Keep the mock executor first-class.** Every test case runs against mock first. Real CLI is a second smoke pass. Hadron's real-only model is an anti-goal; Fragments-Engine's unsafe_mode gate is a warning.

**D9 — Queue.db stays.** The separate `queue.db` file was a design choice for hot writes (migration 003 comment). Path B keeps it. No consolidation work.

**D10 — Graceful shutdown order.** On SIGINT: (1) stop accepting new HTTP requests, (2) cancel scheduler context, (3) wait for in-flight runs to drain or time out, (4) close bridge, (5) close HTTP, (6) close DB. `scheduler.Stop(ctx)` already handles the worker pool shutdown.

---

## Data flow — live run

1. User clicks "Create task" in GUI.
2. `POST /api/v1/tasks` creates task with `status=todo`.
3. On next scheduler tick (≤1s), picker finds the task, dispatches.
4. Scheduler: `TransitionTask(id, "doing")` → `AppendRunEvent("task_transitioned", {from: todo, to: doing})` → `CreateRun` → `AppendRunEvent("run_started")`.
5. Executor launches, emits log lines via `cb`.
6. `cb` handler: `AppendRunEvent("log", {line})` + `bus.Publish(SchedulerEvent{Type: "run.event", ...})`.
7. Bridge goroutine reads bus → `hub.Broadcast("run.event", data)`.
8. GUI's open SSE subscribers receive the event; task detail page re-renders.
9. Executor emits `CLOCKWORK_ARTIFACT {type: "diff", content: "..."}`.
10. `cb` handler: appends to `result.Artifacts`, `CreateArtifact` (existing), `AppendRunEvent("artifact", {...})`, publishes.
11. Executor emits `CLOCKWORK_DONE`.
12. Run completes, `result.Status = "done"`, `result.Artifacts` populated.
13. Result drains to lifecycle: `HandleResult(taskID, runID, result)`.
14. Lifecycle: deliverables check — `result.Artifacts` has `type=diff`, requirement met → `transition(task, "done", "")`.
15. Lifecycle: `TransitionTask(id, "done")` → `AppendRunEvent("task_transitioned", {from: doing, to: done})` → `bus.Publish("task.transitioned", ...)`.
16. GUI re-renders task as done; run events list shows full log; artifacts tab shows the diff.

---

## Error paths

- **Executor process crash** — `runtime/scheduler/scheduler.go:246-252` already handles this (returns error, completes run with `failed`, fires lifecycle). Path B additions: persist a `run_event` of type `error` with the error message; lifecycle transitions task per `on_fail` rule.
- **Missing required deliverable** — lifecycle's `retryOrBlock` either retries (if budget remains) or transitions to `blocked` with a clear reason. Path B verifies this end-to-end.
- **Timeout** — `executor-cli/plugin.go:127-131` already handles `runCtx.Err()` → status `failed`, reason `execution timeout`. Path B: verify this surfaces correctly through the run event log.
- **Cost ceiling reached** — `scheduler.go:126-135` already pauses dispatch. Path B: publishes a `scheduler.cost_ceiling` bus event (new) so GUI can warn the user.
- **Stale worker** — `scheduler.go:156-168` already detects and publishes `worker.stale`. Path B: verify bridge forwards it, GUI surfaces it.
- **Queue backlog depth** — `Status()` already reports `queue_depth`. GUI scheduler status widget displays it.

---

## Testing strategy

### Unit tests

- `sqlstore/runs_test.go` — new tests for `AppendRunEvent`, `ListRunEvents`, cursor behavior
- `scheduler_test.go` — extend existing roundtrip test to verify run events are written
- `executor-cli/plugin_test.go` — verify artifact signals populate `result.Artifacts`
- `executor-api/plugin_test.go` — same verification

### Integration tests

- `scheduler/integration_test.go` — extend `TestIntegrationFullRoundTrip` to assert `run_events` row count and types
- `scheduler/deliverables_e2e_test.go` — new file, the 5 test cases from Component 9
- `httpserver/sse_bridge_test.go` — new, verify bridge forwards events correctly
- `httpserver/runs_test.go` — new, verify `GET /api/v1/runs/{id}/events` with cursor pagination

### Smoke tests (manual)

- Mock smoke: create task via `curl POST /api/v1/tasks`, watch status transition via `curl /api/v1/tasks/{id}` — run with mock executor.
- Real CLI smoke: same task with a profile pointing at a minimal shell script that echoes `CLOCKWORK_DONE`. Verify full run record + event log.
- GUI smoke: open task detail page, create a task, watch live status + event stream update. (Deferred to GUI session.)

---

## Out of scope (still)

- Multi-process / multi-node scheduler. Single process only.
- Workflow / pipeline tasks (DAG of tasks). One task, one run.
- Run cancellation UI. Primitive (`pool.Stop`, `executor.Kill`) exists; GUI surface is deferred.
- Artifact content rendering in GUI (diff viewer, file tree, etc.) — primitives land in Path B, viewer is a design pass.
- Cost analytics / per-sprint budgets. Primitives exist; UI deferred.
- Task prioritization GUI / drag reorder.
- Historical replay of run events through a different executor version.
- Executor plugin hot-reload.

---

## Follow-ups (after Path B lands)

1. PUT vs PATCH harmonization on task/tag endpoints (boot prompt item 4)
2. `taskJSON` timestamp serialization consistency (boot prompt item 4)
3. N+1 task-tag loading in `tasksJSON` (boot prompt item 4)
4. Task list endpoint filters on new Project-2 fields (boot prompt item 4)
5. Structured artifact content renderers in GUI
6. Run cancellation UI
7. Cost budget dashboard

---

## Path A boundary (what A must ship to stay safe for B)

Path A is the strict subset. The invariants Path A **must** respect so Path B extends cleanly:

- **Component 1 (scheduler in serve)** — ship in full. No half-measures; delete `clockworkd` in A too if possible.
- **Component 2 (scheduler HTTP handlers)** — ship in full. Trivial once scheduler exists.
- **Component 3 (Store run_events methods)** — **ship at least `AppendRunEvent` + `ListRunEvents`** in A. A doesn't need pagination in the GUI, but the methods must exist so scheduler can write.
- **Component 4 (scheduler writes run events)** — ship in A. Even if the GUI doesn't read them yet, the data must be captured from day one. Otherwise Path B has no history to work with for runs that happen between A and B.
- **Component 5 (executor artifact plumbing)** — **ship in full in A.** This is the easiest to defer and the most expensive to defer. If A ships with the `result.Artifacts` gap, the deliverables gate doesn't work, which means A's integration tests don't cover the deliverables path, which means Path B finds the gap during its own tests and has to retrofit test fixtures.
- **Component 6 (`RunID` in `WorkerResult`)** — ship in A. Bug fix, trivial, prevents downstream cascade.
- **Component 7 (SSE bridge)** — ship a **minimal** version in A that forwards `task.transitioned` + `run.started` + `run.completed`. Path B extends to forward everything; the shape is the same.
- **Component 8 (run events HTTP API)** — **defer to Path B.** Not needed for Path A's success criterion.
- **Component 9 (deliverables E2E tests)** — ship the mock cases in A. Real CLI case in B.
- **Component 10 (task-scoped SSE filter)** — defer to Path B.
- **Component 11 (GUI wiring)** — defer to frontend session after B backend lands.

This means **Path A's surface is ~70% of Path B's code**, but only ~30% of Path B's *new concepts*. The only pure B-only work is: Component 8 (run events HTTP), expanded event types in Component 7, and Component 9's real CLI test. Everything else is built in A and extended in B.

---

## Risks

- **Scheduler goroutine lifecycle during tests.** `httpserver` tests currently don't need a scheduler. Decision: make the scheduler parameter optional (nil-safe) in `httpserver.New`, so existing tests don't need to construct one. Handlers that need it return 503 if nil.
- **SQLite write contention under load.** Today's scheduler persists runs and artifacts; Path B adds run events (potentially many per run). Write-ahead logging (WAL) is already enabled (`appdb.Open`). Batch inserts if benchmarks show a bottleneck — probably not needed at our scale.
- **Bridge goroutine leak on shutdown.** `SchedulerBridge.Run` must exit on context cancel; if it blocks on bus receive after bus closes, we leak. Handled via the `ok := <-sub` pattern.
- **Real CLI smoke test depends on a profile.** Need a minimal shell-script profile that emits `CLOCKWORK_DONE`. Either ship one as an example or generate it in the test. Prefer shipping one — it's also a user-facing example.
- **Migration drift.** `run_events` table exists in migration 003 but the comment references a "write buffer drain goroutine" that doesn't exist. Two options: (a) delete the stale comment, (b) actually build the write buffer. Path B picks (a) — direct writes are simpler and the contention risk is theoretical.

---

## Open questions (resolve during Path A execution)

1. **Should lifecycle transitions write `run_events` or `task_events`?** Currently the table is `run_events` but task transitions aren't scoped to a run (e.g., `todo → doing` happens *before* the run exists). Options:
   - (a) Write task transitions with `run_id = 0` (sentinel for "no run")
   - (b) Add a separate `task_events` table
   - (c) Make `run_id` nullable and use NULL for pre-run transitions
   - **Lean:** (c). Nullable is the honest shape. Small migration.
2. **Does the GUI need a dedicated `/api/v1/tasks/{id}/events` endpoint, or is client-side filter enough?** Covered by D6; lean client-side.
3. **Do we need a typed event schema (Go struct) or is JSON blob OK?** Lean JSON blob with a `type` discriminator, structured payload per type. Typed structs are expensive to maintain and the GUI parses JSON anyway.

---

## Exit

When Path B ships:
- A new backend session boot prompt replaces the E2E goal with "observability polish + deferred follow-ups."
- GUI session picks up Component 11 (task detail page live updates).
- `BLG-20260409-001` can be closed as shipped.
