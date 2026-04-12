# E2E Execution Prior Art — Reference

**Date:** 2026-04-11
**Author:** Backend session (brainstorm)
**Status:** Reference doc — supports Path A / Path B specs below
**Related:**
- `docs/superpowers/specs/2026-04-11-e2e-execution-path-a.md` (fast happy path)
- `docs/superpowers/specs/2026-04-11-e2e-execution-path-b.md` (iteration-ready loop)

## Purpose

Clockwork-Manifold's scheduler → executor → artifact → result loop is being wired for real end-to-end task runs (`BLG-20260409-001` and the boot-prompt E2E goal). Three sibling Go / PHP projects already have production runner patterns. This doc captures the load-bearing ideas so Path A/B designs can borrow instead of reinvent.

---

## Hadron — closest analog

Hadron is the closest structural match: Go, SQLite-backed run records, worker pool, scheduled triggers. No deliverables gate — that's a novel feature in clockwork-manifold.

### Run lifecycle

- Runs are created via `execution.Request` and enqueued onto a worker pool queue (128-slot buffer).
- Worker goroutines pop requests and execute them via `Manager.worker()`.
- States: `queued` → `started` → `finished` (with `status: success|failed`).
- Persisted via `SetRunStarted()` / `SetRunFinished()` in SQLite. `error_message` optional.
- `scheduler.engine.go` ticks at 1s, queries "due" schedule records, claims each via optimistic locking, enqueues an `execution.Request` with the cron-computed next run.

### Artifact capture

- Runs emit `RunEvent` records via `AppendRunEvent()` during execution — step stdout, status changes, typed events.
- Each event has `type`, `message`, `step_name`, `created_at`. Append-only.
- Pipeline stages emit a `StageResult` struct: `Status`, `Outputs map[string]any`, `ExitCode`, `Stdout`, `Stderr`. Outputs JSON-serialised and persisted via `UpdatePipelineStageRunOutputs()`.
- **No deliverables gate** — outputs stored post-execution; caller interprets them.

### Executor dispatch

- Manager is a worker pool (configurable N). Buffered queue decouples scheduler tick from execution.
- Each worker calls task-specific executor logic via PTY (pseudo-terminal) and streams output line-by-line with `bufio.Scanner`.
- Output lines broadcast to any registered subscribers. `Subscribe()` returns a buffered channel; events fan out on every line.

### HTTP/SSE surface

- `/v1/runs` lists runs; `/v1/runs/{id}` gets run + events.
- Events endpoint supports time range + cursor pagination — enables polling from the frontend without re-streaming.
- Subscriber channels support live broadcast, but the REST API is polling-based. No native SSE endpoint in `api/server.go`.

### Smoke testing

- Real CLI execution (no mock). Timeout + `context.Context` enforce safety.

### File pointers

- `/Users/chrispian/Projects-apps/hadron/internal/execution/manager.go` — worker pool, subscriber pattern, event broadcast
- `/Users/chrispian/Projects-apps/hadron/internal/scheduler/engine.go` — schedule polling, claim & dispatch
- `/Users/chrispian/Projects-apps/hadron/internal/persistence/store.go` — run lifecycle, event persistence
- `/Users/chrispian/Projects-apps/hadron/internal/pipeline/runner.go` — stage execution, output capture, topo sort
- `/Users/chrispian/Projects-apps/hadron/internal/api/server.go` — REST surface

---

## Nanite — simpler, cleanest broadcaster

Nanite's pipeline executor is the smallest of the three and has the cleanest fan-out pattern. Worth borrowing verbatim for the `EventBus → SSEHub` bridge.

### Run lifecycle

- `Executor.Run()` is **synchronous** — returns `RunState` on completion. No persistent queue; caller blocks until done or context cancels.
- States: `pending` → `running` → `completed|failed|cancelled`. Per-step states track progress inside the run.

### Artifact capture

- Each step returns `StepOutput`: `Data map[string]any`, `Stdout`, `Stderr`, `ExitCode`.
- Outputs collected into a `priorResults` map and passed to dependent steps.
- **No separate artifact store** — outputs live in memory during the run and are lost after completion. Explicitly unsuitable as a long-term persistence model, but the in-memory aggregation shape is useful.

### Executor dispatch

- Single-threaded topological-level executor.
- Steps grouped by dependency level; levels execute sequentially, steps within a level run concurrently via `sync.WaitGroup`.
- Per-step `GateFunc` checks prior results before proceeding — a minimal lifecycle gate.

### HTTP/SSE surface — **the broadcaster pattern to borrow**

```go
type Broadcaster struct { /* ... */ }

func (b *Broadcaster) Subscribe() <-chan Event
func (b *Broadcaster) Unsubscribe(ch <-chan Event)
func (b *Broadcaster) Publish(e Event)  // non-blocking; drops on full buffer
```

- Buffered channels (64-slot).
- `Publish` uses a non-blocking `select` — if a subscriber's buffer is full, the event is dropped **for that subscriber only**. Publisher never blocks.
- Event types: `pipeline.started`, `step.skipped`, `step.completed`, `pipeline.cancelled`.
- This is structurally identical to what `internal/runtime/scheduler/events.go:57-75` already does in clockwork-manifold. Pattern is already adopted; we just need to bridge it to HTTP.

### Smoke testing

- `StepHandler` interface: any type implementing `Execute()` can be wired in. No lock-in to CLI. Mock handlers used in tests.

### File pointers

- `/Users/chrispian/Projects-apps/nanite/internal/workflow/executor.go` — topo sort, level-based concurrency, event emission
- `/Users/chrispian/Projects-apps/nanite/internal/workflow/workflow.go` — step/run state definitions, gate + retry policy
- `/Users/chrispian/Projects-apps/nanite/internal/workflow/broadcaster.go` — non-blocking multicast

---

## Fragments-Engine — lifecycle-hook inspiration

Older and larger. Most of its value is the **lifecycle-hook model** that inspired clockwork-manifold's `on_done` / `on_fail` rules. Validates the design choice rather than providing new code to borrow.

### Run lifecycle

- Scheduler (`scheduler/runner.go`) claims tasks from DB via `claimNextTodo()` with optimistic locking.
- Wraps task in an agent session context, executes via external provider (e.g. Claude CLI), updates task status.
- States: `todo` → `doing` → `review|done|blocked`. Boundary rules gate transitions.
- Orphan recovery on startup resets stale `doing` → `todo`.

### Artifact capture

- Executor output captured as structured result.
- Artifacts promoted via CLI (`volon artifact promote`) — copies to `docs/`, injects `promoted_at` frontmatter, logs to `.agentrc/logs/`.
- **No in-process artifact callback** — the CLI is responsible for writing. This is the opposite end of the spectrum from Hadron's event-stream approach. Clockwork should prefer Hadron's model.

### Executor dispatch

- Multi-worker scheduler loop. Each tick tries `claimNextTodo()` with optimistic locking.
- Executor wraps providers (Claude, local CLI). Signal tokens handled for interrupts.
- `LogBroadcast` callback streams task logs line-by-line to the HTTP server for SSE. **This is the same seam clockwork's scheduler has** — executor → callback → bus → HTTP.

### Lifecycle hooks

- Plugin event system: `task.created`, `task.transitioned`, `task.completed`, `sprint.completed`.
- Boundary rules gate transitions: `on_complete`, `on_block`, `on_escalation`.
- Prevents implicit auto-promotion without review.

### Smoke testing

- Real external executor only (`unsafe_mode` flag gate). No mock path.
- Noted as a pain point — makes it hard to run integration tests in CI.

### File pointers

- `/Users/chrispian/Projects-apps/fragments-engine/engine/cmd/scheduler/main.go` — scheduler entrypoint, log broadcast setup, boundary rules
- `/Users/chrispian/Projects-apps/fragments-engine/engine/internal/runtime/scheduler/runner.go` — worker loop, claim/execute/escalate, session lifecycle
- `/Users/chrispian/Projects-apps/fragments-engine/engine/internal/plugin/events.go` — task/sprint event catalog
- `/Users/chrispian/Projects-apps/fragments-engine/engine/internal/taskscli/cli/artifact.go` — artifact promotion

---

## Patterns worth borrowing

### 1. Append-only `run_events` log (Hadron, Fragments-Engine)

Write one row per streamed event (log line, signal, artifact, status change). Keyed by `run_id`. Cheap to query with time-range + cursor pagination. Enables:
- Audit trail without re-running
- Live tailing (subscribe to new rows)
- GUI log viewer that doesn't need to be connected during execution

**Clockwork status:** Table already exists in `migrations/003_concurrency.sql:3`. No Go wrapper, no scheduler integration. Comment on the migration says "drained from queue.db into this table for persistence" — implies a future write-buffer architecture that never landed. For now we can write directly from the scheduler's event callback.

### 2. Broadcaster / non-blocking fan-out (Nanite)

Pattern: publisher never blocks on slow subscribers; slow subscribers drop events silently. Already implemented in `internal/runtime/scheduler/events.go:57-75` (`EventBus.Publish`). Also independently implemented in `internal/httpserver/sse.go:30-46` (`SSEHub.Broadcast`). **Two implementations of the same pattern with no bridge.** Fix is a single adapter goroutine.

### 3. Worker pool + buffered queue (Hadron)

Decouples scheduler tick from execution. Already implemented in `internal/runtime/scheduler/worker.go`. Clockwork matches Hadron here.

### 4. Lifecycle boundary rules (Fragments-Engine)

Gate task transitions on `on_done` / `on_fail` rules. Already implemented in `internal/runtime/scheduler/lifecycle.go`. Clockwork matches.

### 5. Deliverables gate (novel to clockwork)

No prior art in any of the three repos. Half-wired at `internal/runtime/scheduler/lifecycle.go:54-66`. Needs `result.Artifacts` to be populated — currently executors stream artifacts via callback but don't append to the result struct, so the gate never sees them.

---

## Anti-patterns to avoid

- **Polling-only HTTP surface (Hadron).** Fine for Hadron's audit-first use case, wrong for clockwork-manifold's "watch your task run" UX. We already have SSE infrastructure; use it.
- **Synchronous in-memory run state (Nanite).** Fine for Nanite's short-lived pipelines, wrong for multi-minute LLM task runs. We already persist runs; keep doing that.
- **Mock-free smoke testing (Fragments-Engine).** We already have `MockExecutor` in `internal/runtime/executor/mock.go`. Keep the mock path and add a real-CLI path alongside it.

---

## Patterns to cross-reference during implementation

When writing run event persistence:
- `internal/runtime/scheduler/scheduler.go:224-242` — where the executor callback fires today (currently only broadcasts via bus, doesn't persist)
- `internal/runtime/scheduler/events.go:57-75` — the broadcast side

When wiring the SSE bridge:
- `internal/httpserver/sse.go:30-46` — target hub
- `internal/httpserver/server.go:107` — where `/api/v1/events` is mounted

When writing the deliverables plumbing fix:
- `internal/runtime/executor/types.go:83-90` — `ExecutionResult` struct
- `plugins/executor-cli/plugin.go:212-215` — where artifact signals are dispatched today (via cb only, not appended to result)
- `internal/runtime/scheduler/lifecycle.go:54-66` — the consumer that needs populated artifacts

---

## Named decisions (from this survey)

**D1 — Borrow Hadron's append-only run_events model, not Fragments-Engine's CLI-promotion model.** Clockwork needs in-process event capture for live observability; CLI promotion is too slow and too coupled to a specific workflow.

**D2 — Use the existing `EventBus`, bridge to existing `SSEHub`.** Don't add a third broadcaster. One adapter goroutine closes the gap.

**D3 — Keep the mock executor path first-class.** Every test / smoke run goes through mock first; real CLI is a second smoke step. Avoid Fragments-Engine's unsafe_mode-only pain.

**D4 — `result.Artifacts` must be populated during execution**, not just streamed. The deliverables gate and the run_events persistence both need this. Fix it once, in the executor plugins, early in Path A.

**D5 — Single process (clockwork serve).** Hadron and Fragments-Engine both run scheduler + HTTP in one process. Clockwork's split (`clockworkd` + `clockwork serve`) is an accident of staged development, not a design choice.
