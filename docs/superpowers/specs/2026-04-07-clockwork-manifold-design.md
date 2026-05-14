# Torque — Design Specification

**Date:** 2026-04-07
**Status:** Draft
**Authors:** Chrispian + Claude (brainstorm session)

---

## 1. Overview

Torque (short: Torque) is a standalone task orchestration and execution engine. It manages tasks through a full finite state machine, schedules them via a persistent multi-worker queue, and delegates execution to pluggable executor backends. Everything beyond task management and execution is a plugin.

Torque replaces Fragments Engine (formerly Volon). It is a clean rebuild — not a rename or refactor — taking the best of Fragments Engine's task/scheduler system and Nanite's executor/permission/sandbox patterns, discarding legacy baggage.

### Identity

| Property | Value |
|---|---|
| Full name | Torque |
| Short name | Torque |
| Repo | `hollis-labs/torque` |
| Module | `github.com/hollis-labs/torque` |
| CLI binary | `torque` |
| MCP prefix | `mcp__torque__*` |
| Signal prefix | `TORQUE_*` |
| Env prefix | `TORQUE_*` |
| Data directory | `.torque/` |
| OTel tracer | `torque/*` |
| Local path | `~/Projects-apps/torque` |

### What Torque Is

- A standalone task orchestration + execution engine
- Tasks are self-contained execution contracts with full context
- Runner/queue is the core differentiator — persistent, durable, observable
- Plugin-extensible for everything beyond task management and execution
- GUI is tasks-first with opt-in complexity layers
- MCP-native — any harness can use it
- Agent-assisted recovery at high-value decision points
- Opinionated about scheduling and lifecycle, unopinionated about who does the work

### What Torque Is NOT

- A chat interface (plugin if someone wants it)
- An agent identity/personality system (plugin territory)
- A provider management layer (executor plugins via go-providers)
- A context memory system (that's Vanta Conduit)
- A blueprint automation engine (that's Hadron)
- An interactive AI workspace (that's Nanite)
- A service lifecycle manager (that's Cerberus)

### Ecosystem Position

```
User's harness (Claude Code, Codex, Copilot, Nanite, etc.)
    | MCP
Torque (tasks, queue, execution)
    | executor plugins          | go-queue        | plugins
  CLI / API / custom        job scheduling      Nexus, GitHub, Slack, etc.
    | go-providers
  Anthropic, OpenAI, Ollama, etc.
```

### Shared Libraries Consumed

- **go-plugin** — Plugin SDK (same as Nanite)
- **go-queue** — Queue backend (SQLite, memory, pluggable drivers)
- **go-mcp** — MCP helpers, budget envelopes
- **go-otel** — OpenTelemetry observability
- **go-toolbroker** — Tool selection for executor-api
- **go-providers** — Consumed by executor plugins, not core

---

## 2. Repository Structure

```
torque/
├── cmd/
│   ├── torque/        # Primary CLI + MCP server
│   └── torqued/       # Scheduler daemon
├── internal/
│   ├── persistence/      # sqlstore, migrations
│   ├── service/          # Business logic (task-centric)
│   ├── runtime/
│   │   ├── scheduler/    # Queue + worker orchestration
│   │   └── executor/     # Executor plugin interface
│   ├── mcpadapter/       # MCP tool exposure
│   ├── plugin/           # Plugin host (go-plugin SDK)
│   ├── tool/             # Tool interface, registry, safety inference
│   ├── permission/       # Permission engine, rules, approval flow
│   ├── sandbox/          # Execution isolation, env filtering, network proxy
│   ├── toolrouter/       # MCP tool routing, broker integration
│   └── config/           # Settings, preferences
├── plugins/
│   ├── executor-cli/     # CLI executor (claude, codex, etc.) — ships default
│   ├── executor-api/     # API executor (Anthropic, OpenAI direct) — ships default
│   └── core/             # Task CRUD handlers
├── apps/
│   └── gui/              # React/Tauri GUI (tasks-first)
├── api/
│   └── proto/            # gRPC definitions (torque.tasks.v1)
├── docs/
└── .agentrc/
```

**Two entrypoints:**
- `torque` — CLI + MCP server + `torque serve` (GUI HTTP server)
- `torqued` — Scheduler daemon

The gui-server is a subcommand of `torque serve`, not a separate binary. Chat is gone from core entirely.

---

## 3. Core Data Model

### Design Principle

Tasks are the first-class citizen. Everything else is opt-in. A task is a self-contained execution contract — it contains everything an executor needs to run it with no ambient context required.

### Task Record

```
-- Identity --
ID                    CW-YYYYMMDD-NNNN (auto-generated)
Title                 string (required)
Description           string (required — the detailed prompt, as long as needed)
Tags                  []string
Priority              P1 / P2 (default) / P3

-- Status --
Status                todo | doing | review | done | blocked | paused | archived
Manual                bool (scheduler skips if true)

-- Execution Context --
Executor              string — which executor plugin ("cli", "api", custom)
AgentProfile          string — executor-specific config (model, CLI flags, etc.)
WorkingDir            string — repo/project path for this task
Tools                 []string — allowed MCP tools / tool groups (optional, nil = executor default)
Permissions           map — permission overrides (optional)
Environment           map — env vars to inject (optional)
SystemPrompt          string — additional system prompt context (optional)
Files                 []string — specific files/paths relevant (optional)

-- Budget & Limits --
CostBudget            float — max spend (optional, nil = sprint/global default)
MaxRetries            int — retry count on failure (default from settings)
MaxDuration           duration — timeout for a single run (optional)
TokenBudget           int — max tokens per run (optional)

-- Lifecycle Rules --
OnDone                "close" | "review" | "notify" (default: "review")
OnFail                "retry" | "block" | "escalate" | "notify" (default: "retry")
OnReview              "pause" | "notify" | "auto-approve" (default: "pause")
EscalationChain       []string — ordered: retry -> senior-agent -> council -> human (optional)
QualityGates          []string — commands to run on completion before accepting (optional)

-- Deliverables --
Deliverables          []Deliverable — required outputs (optional, nil = no requirements)
DeliverablePreset     string — reference to a named preset (optional)

-- Merge --
OnDoneMerge           "none" | "auto" | "pr" | "auto-resolve" (default: "none")

-- Dependencies --
DependsOn             []taskID — blocked until these complete
BlockedReason         string — if status=blocked, why

-- Metadata --
Metadata              map[string]any — flexible key-value (resolution pointers, custom data)

-- Grouping (opt-in, nullable) --
SprintID              *string
ProjectID             *string
EpicID                *string

-- Audit --
CreatedAt             timestamp
UpdatedAt             timestamp
```

**Task status FSM:**

```
todo --> doing --> review --> done
           |        |
           v        v
      blocked / paused

Any state --> archived (explicit action)
```

Tasks can become `blocked` or `paused` from either `doing` (executor reports blocked) or `review` (reviewer pauses). `blocked` can return to `todo` when the blocker is resolved. `paused` can return to `todo` when explicitly resumed.

No `backlog` or `queued` states. Backlog is a tag or view filter, not a state.

### Deliverables

Tasks can declare required evidence before accepting a status transition.

```
Deliverable {
  Type          string — artifact type
  Required      bool — block transition if missing (default: true)
  Description   string — what specifically is needed
}
```

**Built-in artifact types:**

| Type | What it is |
|---|---|
| `diff` | Git diff or patch content |
| `test-results` | Test runner output (pass/fail/skip + detail) |
| `screenshot` | Image file (before/after, UI state) |
| `pr-link` | URL to pull request |
| `branch` | Branch name created |
| `commit` | Commit SHA(s) |
| `log` | Execution log / terminal output |
| `finding` | Analysis result or recommendation |
| `report` | Longer-form written output |
| `note` | Freeform text |
| `metrics` | Structured key-value data (tokens, coverage, lines changed) |
| `custom` | Plugin-defined type |

**Deliverable presets** (stored in settings):

```yaml
deliverable_presets:
  frontend-fix:
    - { type: diff, required: true }
    - { type: test-results, required: true }
    - { type: screenshot, required: true, description: "Before and after" }
    - { type: pr-link, required: false }

  backend-fix:
    - { type: diff, required: true }
    - { type: test-results, required: true }
    - { type: pr-link, required: false }

  research:
    - { type: finding, required: true }
    - { type: note, required: false }

  refactor:
    - { type: diff, required: true }
    - { type: test-results, required: true }
    - { type: metrics, required: false, description: "Lines added/removed, files changed" }
```

### Artifact Record

```
Artifact {
  ID            auto
  TaskID        FK
  RunID         FK (which execution produced this)
  Type          string (from built-in types)
  Content       text (diff content, log output, finding text, JSON metrics)
  URL           string (for pr-link, screenshot hosted URL)
  FilePath      string (for local files)
  Metadata      map (flexible key-value)
  CreatedAt     timestamp
}
```

### Opt-in Layers

Off by default, enabled in settings:

| Layer | Setting key | What it adds |
|---|---|---|
| Sprints | `features.sprints` | Time-boxed groupings, approval modes, cost budgets |
| Projects | `features.projects` | Logical groupings with repo path association |
| Epics | `features.epics` | Long-lived cross-sprint groupings |

When off: MCP tools not registered, GUI sections hidden, DB tables exist but unused, tasks have nullable FK columns.

When on: MCP tools appear, GUI shows views, task create/update forms expose association fields.

### Supporting Records

- **Runs** — execution history: task ID, executor, start/end, tokens, cost, exit status
- **Comments** — on tasks (and sprints/epics/projects when enabled)
- **Settings** — key-value store for feature flags, defaults, executor config

---

## 4. Executor Plugin Architecture

### Design Principle

Torque core doesn't know how to run a task. It schedules, tracks, and manages lifecycle. Execution is delegated to executor plugins via a clean interface.

### Executor Interface

```go
type Executor interface {
    Name() string
    Run(ctx context.Context, job *ExecutionJob, cb EventCallback) (*ExecutionResult, error)
    Capabilities() ExecutorCapabilities
    Validate(job *ExecutionJob) error
}

type ExecutionJob struct {
    TaskID        string
    Description   string
    SystemPrompt  string
    WorkingDir    string
    AgentProfile  string
    Tools         []string
    Permissions   map[string]string
    Environment   map[string]string
    Files         []string
    Deliverables  []Deliverable
    Limits        ExecutionLimits
}

type ExecutionLimits struct {
    CostBudget   *float64
    TokenBudget  *int
    MaxDuration  *time.Duration
    MaxRetries   int
}

type EventCallback func(event ExecutionEvent)

type ExecutionEvent struct {
    Type      EventType   // Log, Signal, Artifact, Progress, TokenUsage
    Signal    string      // TORQUE_DONE, TORQUE_BLOCKED, etc.
    Content   string
    Artifact  *Artifact
    Tokens    *TokenUsage
    Progress  *float64    // 0.0-1.0
}

type ExecutionResult struct {
    Status     string        // "done", "review", "blocked", "failed"
    Reason     string
    Artifacts  []Artifact
    Tokens     TokenUsage
    Cost       float64
    Duration   time.Duration
}

type ExecutorCapabilities struct {
    SupportsStreaming   bool
    SupportsTools       bool
    SupportsSandbox     bool
    SupportsPermissions bool
}
```

### Tool Execution Layer (Core)

Sandbox and permissions live in core, not executor plugins. Any executor that needs tool execution calls back into core:

```
Scheduler -> Executor (CLI or API) -> LLM decides on tool call
                                          |
                               Core Tool Router (permissions + sandbox)
                                          |
                               MCP tool execution (sandboxed)
```

- **executor-cli**: Spawns CLI process. CLI handles its own tool execution internally. Torque sandboxes the CLI process itself (restricted env, secrets stripped).
- **executor-api**: Calls LLM API directly. Tool calls route through core's tool router which handles permission checks and sandboxed execution.

Core tool/permission/sandbox packages are ported from Nanite's patterns:
- Tool interface with safety inference (IsReadOnly, IsDestructive from tool name)
- Permission engine (3-tier rules: deny > ask > allow, approval flow with scopes)
- Sandbox isolation (OS-level, env filtering, secret stripping, network proxy allowlist)

### Shipped Executor Plugins

**executor-cli** (default):
- Spawns CLI processes (claude, codex, copilot, gemini, generic)
- Subprocess by default, PTY opt-in per agent profile
- Parses stdout for `TORQUE_*` signals
- Sandbox: restricted env, secret filtering, network proxy
- Agent profiles = CLI configs (binary, model flags, args)
- Quality gates as post-execution shell commands

**executor-api**:
- Calls LLM APIs via go-providers
- Tool calling via core tool router (MCP tools + configured external servers)
- Permission checking via core permission engine
- Agent profiles = provider + model + temperature + system prompt

### Third-Party Executors

Anyone can implement the `Executor` interface. LangChain, CrewAI, custom internal systems — write a plugin, register it, set `executor: "your-plugin"` on tasks.

---

## 5. Plugin System

### Design Principle

Same go-plugin SDK as Nanite. Full extension surface. Same catalog/registry model. Plugins can extend UI, data, execution, lifecycle, and filtering. Implement what Torque needs now; the rest of the SDK surface is available when demand arrives.

### Plugin Host Interface

```go
type Host interface {
    // Services
    GetService(name string) interface{}

    // CRUD
    RegisterCRUDHandler(resourceType string, handler CRUDHandler)

    // Events & Hooks
    RegisterEventHook(event string, hook EventHook)

    // Executors
    RegisterExecutor(executor Executor)

    // Tools
    RegisterTools(tools []ToolDefinition)

    // UI Components
    RegisterUIComponent(slot string, component UIComponent)

    // Filters
    RegisterFilter(chain string, filter Filter)

    // Connectors
    RegisterConnector(name string, connector Connector)
    ConnectorHealth(name string) HealthStatus

    // Config
    GetPluginConfig(pluginID string) map[string]interface{}
    SetPluginConfig(pluginID string, config map[string]interface{})
}
```

### Day 1 Implementation vs. Available Surface

| Capability | Day 1 | Available when needed |
|---|---|---|
| CRUD handlers | Yes | Any resource type |
| Event hooks | Yes — task/run/artifact lifecycle | Custom events |
| Executor registration | Yes | |
| Tool registration | Yes | |
| UI slots | Yes — Torque-specific slots | Additional slots |
| Filters | Yes — minimal set | Additional chains |
| Connectors | Interface ready | Plugins bring their own |
| Catalog/registry | Yes — same as Nanite | |
| Config | Yes | |

### Lifecycle Events

| Event | When |
|---|---|
| `task.created` | Task inserted |
| `task.updated` | Task fields changed |
| `task.transitioning` | Before status change |
| `task.transitioned` | After status change |
| `task.assigned` | Executor picked up task |
| `run.started` | Execution begins |
| `run.completed` | Execution finishes |
| `run.failed` | Execution failed |
| `artifact.created` | Artifact attached |
| `scheduler.tick` | Each scheduler cycle |
| `deliverable.check` | Before transition acceptance |

### UI Slots

| Slot | Location | What plugins add |
|---|---|---|
| `TaskDetail` | Task detail panel | Extra tabs, widgets, actions |
| `TaskList.Actions` | Task list toolbar | Bulk actions, filters |
| `TaskList.Columns` | Task list table | Custom columns |
| `Dashboard` | Main dashboard | Widgets (charts, summaries) |
| `Settings` | Settings page | Plugin config tabs |
| `NavRail` | Navigation sidebar | New pages/sections |

### Filter Chains

| Chain | When | What filters do |
|---|---|---|
| `task.before_create` | Before task persisted | Validate, enrich, transform, reject |
| `task.before_transition` | Before status change | Block, redirect, add conditions |
| `execution.before_run` | Before executor starts | Modify job, inject context, gate |
| `artifact.before_attach` | Before artifact saved | Validate, transform, redact |

Filters are priority-ordered. They can modify the payload or short-circuit with an error.

### Catalog & Discovery

Same model as Nanite: `CatalogFetcher` with multi-source merging, signature verification, priority-ordered sources. `torque plugin install <name>` pulls from catalog, verifies, installs. Local discovery via `plugins/` directory with `plugin.yaml` manifests.

### Ecosystem Note

Each app (Nanite, Torque, Hadron) has its own slots, filter chains, and connector needs. The SDK provides the mechanism. The app defines what's meaningful. A plugin built for Nanite's `Composer` slot won't work in Torque — that's expected and correct.

---

## 6. Scheduler & Queue

### Design Principle

The scheduler is Torque's core differentiator. Persistent, multi-worker orchestration that turns tasks into completed work. Built on go-queue for the queue layer, with the scheduler providing intelligence on top.

### Architecture

```
torqued (scheduler daemon)
    |
    +-- Queue (go-queue)
    |   +-- SQLite driver (default)
    |   +-- Memory driver (dev/testing)
    |   +-- Pluggable (Postgres, Redis via go-queue drivers)
    |
    +-- Scheduler
    |   +-- Worker pool (configurable concurrency)
    |   +-- Task picker (priority + dependencies + cost budget)
    |   +-- Executor dispatch (delegates to executor plugins)
    |   +-- Lifecycle manager (applies OnDone/OnFail/OnReview rules)
    |   +-- Deliverable checker (validates required artifacts)
    |   +-- Escalation engine (retry -> escalate chain)
    |   +-- Cost tracker (per-task, per-sprint, global)
    |
    +-- Event emitter (broadcasts to plugin hooks + GUI via SSE)
```

### Scheduler Loop

```
1. Query eligible tasks:
   - Status = todo
   - Manual = false
   - Dependencies met (all DependsOn tasks are done)
   - Not over sprint/global cost budget
   - Ordered by: priority ASC, created_at ASC

2. For each eligible task (up to available worker slots):
   a. Transition: todo -> doing
   b. Create Run record
   c. Build ExecutionJob from task fields
   d. Look up executor plugin (task.Executor)
   e. Dispatch to executor with EventCallback
   f. Stream events -> update Run, attach Artifacts
   g. Executor returns ExecutionResult

3. On result:
   a. Check deliverables — required types present?
      Missing -> stay in doing, log what's needed, count as retry
   b. Apply lifecycle rules:
      - done + OnDone=review -> transition to review
      - done + OnDone=close -> transition to done
      - failed + OnFail=retry + retries < max -> re-queue as todo
      - failed + OnFail=escalate -> walk escalation chain
      - failed + OnFail=block -> transition to blocked
      - blocked -> transition to blocked with reason
      - review -> transition to review
   c. Update Run record (tokens, cost, duration, status)
   d. Emit lifecycle events

4. Bookkeeping:
   - Update cost accumulators
   - Check stale workers (heartbeat timeout)
   - Log tick metrics
```

### Worker Pool

Semaphore-based concurrency control (Nanite pattern):

```go
type WorkerPool struct {
    maxWorkers    int
    semaphore     chan struct{}
    activeWorkers map[string]*Worker
    mu            sync.RWMutex
}
```

- Heartbeat per worker (detect stale/hung executions)
- Graceful shutdown — drain active workers on SIGTERM
- Per-worker context cancellation

### Configuration

```yaml
scheduler:
  workers: 3
  interval: 10s
  retry_budget: 3
  cost_ceiling: 50.00
  heartbeat_interval: 15s
  stale_threshold: 5m
  enabled: true
```

---

## 7. Concurrency Model

### Database Concurrency

**Problem:** Multiple concurrent agents hammering SQLite causes corruption even with WAL mode. The write serialization bottleneck is fundamental to SQLite.

**Solution: Split write paths by frequency tier.**

| Tier | Data | Write pattern |
|---|---|---|
| Hot | Run logs, progress, tokens, heartbeats | Continuous during execution |
| Warm | Task transitions, artifacts, run records | Per-task lifecycle |
| Cold | Task creation, sprint/project/epic CRUD, settings | User-initiated, infrequent |

**Architecture:**

```
Hot writes (progress, tokens, heartbeats)
    |
go-queue (queue.db — SEPARATE SQLite file, append-only)
    |
Write worker (single goroutine, drains at controlled pace)
    |
Main database (torque.db — batched writes, e.g. every 1s or 50 items)
```

- `queue.db` is append-only — minimal contention
- Write worker batches: one transaction with N rows instead of N individual INSERTs
- Crash-safe: queued writes survive on disk (go-queue is persistent)
- Backpressure: queue depth is observable, alerts if backing up
- Main database only sees calm, batched, serialized writes

**Direct writes (low frequency, needs immediate consistency):**
- Task status transitions
- Task creation/updates
- Artifact attachment
- Sprint/project/epic CRUD

**Additional SQLite hardening:**
- `PRAGMA busy_timeout = 5000`
- Write serialization via single-writer goroutine (channel-based, all writes go through one goroutine)
- Separate read connections (WAL allows concurrent reads)

**Postgres opt-in:** For users who want it. The split architecture still helps keep log volume out of the transactional database.

### Git/Filesystem Concurrency

**Rule: One worktree per concurrent task execution.**

```
~/Projects-apps/my-project/                       # Main worktree (user's, never touched)
~/Projects-apps/my-project/.torque/worktrees/
    +-- CW-20260407-0001/                          # Agent 1's isolated worktree
    +-- CW-20260407-0002/                          # Agent 2's isolated worktree
    +-- CW-20260407-0003/                          # Agent 3's isolated worktree
```

- Worktree branch = `torque/<taskID>`
- Created on `run.started`, cleaned up based on policy
- Max concurrent tasks per project = configurable (default: 3)

### Merge Policy

Configurable per task:

```yaml
on_done_merge: none | auto | pr | auto-resolve
```

| Policy | Behavior |
|---|---|
| `none` (default) | Leave the branch, human decides |
| `auto` | Attempt merge to target branch, block on conflict |
| `pr` | Create PR automatically (requires plugin-github or similar) |
| `auto-resolve` | Attempt merge, on conflict spawn resolution agent |

### Agent-Assisted Merge Resolution

When `auto-resolve` encounters a conflict:

```
1. Gather context:
   - Failing task's branch, commits, worktree
   - All related tasks that touched same project in this cycle
   - Conflict details (files, conflict markers)
   - Each task's description and deliverables (for intent)

2. Create resolution task automatically:
   - Title: "Resolve merge conflicts: CW-0001 -> main"
   - Description: auto-generated with full context
   - Deliverables: [diff (required), test-results (required)]
   - Files: conflicting file paths
   - WorkingDir: worktree with conflict state
   - SystemPrompt: conflict context, original task intents
   - OnFail: block (no infinite recursion)
   - Priority: P1 (queue jump)
   - Metadata: resolution_for, related_tasks, confidence_threshold

3. Dispatch immediately (priority queue jump)

4. Resolution agent works:
   - Has all worktrees accessible (read-only pointers)
   - Has commit history for each branch
   - Resolves conflicts, runs tests
   - Reports confidence

5. Outcome:
   - Confident (>= threshold) + tests pass -> merge, done
   - Not confident or tests fail -> block, notify human with:
     - What the agent tried
     - Where it got stuck
     - Partial resolution as artifact
     - Clear description of what the human needs to decide
```

**Escalation chain for merges:**

```
auto-merge -> agent resolution -> human review
```

**This pattern is generalizable.** Agent-assisted recovery applies to merge conflicts, test failures, quality gate failures, and other high-value decision points. Spend tokens where judgment matters, not on boilerplate.

### Concurrency Configuration

```yaml
scheduler:
  workers: 3
  max_per_project: 2
  worktree_cleanup: on_merge    # on_merge | on_done | manual
  default_merge_policy: none

merge:
  resolution_executor: cli
  resolution_agent: default
  confidence_threshold: 0.8
  max_resolution_attempts: 1
  notify_on_conflict: true
```

---

## 8. MCP Surface

### Core Tools (always registered)

```
torque_health
torque_settings_get
torque_settings_save

torque_task_create
torque_task_get
torque_task_update
torque_task_delete
torque_task_list
torque_task_search
torque_task_transition
torque_task_bulk_transition

torque_run_list
torque_run_get

torque_artifact_create
torque_artifact_list

torque_comment_add
torque_comment_list

torque_scheduler_status
torque_scheduler_toggle
```

### Opt-in Tools (registered when features enabled)

```
# features.sprints = true
torque_sprint_create / _get / _update / _delete / _list / _approve

# features.projects = true
torque_project_create / _list / _delete

# features.epics = true
torque_epic_create / _get / _update / _delete / _list
```

Plugin-registered tools appear as `torque_<plugin>_<tool>`.

---

## 9. GUI

### Design Principle

Tasks-first. Scoped down from Engine's current GUI. Opt-in complexity. Plugin-extensible via UI slots.

### Default Layout

```
+--------------------------------------------------+
|  Torque                   [Settings]  |
+--------+-----------------------------------------+
|        |                                         |
| Tasks  | Task List (filter by status, priority,  |
|        | tags, executor)                          |
| ----   |                                         |
| Runs   | [Task rows with status indicators]      |
|        |                                         |
| ----   |                                         |
| [+Nav] | Scheduler: Running (2/3 workers)        |
|        |                                         |
+--------+-----------------------------------------+
```

### Core Pages

| Page | Purpose |
|---|---|
| Task List | Primary view — filter, sort, bulk actions, create |
| Task Detail | Full view — description, status, runs, artifacts, deliverables, comments, actions |
| Runs | Execution history — logs, tokens, cost, duration |
| Dashboard | Scheduler health, active workers, cost summary, queue depth |
| Settings | Feature toggles, executor config, scheduler config, plugin management |

### Feature-Gated Pages

| Feature | Adds to nav |
|---|---|
| Sprints | Sprint list, sprint detail, sprint board view |
| Projects | Project list, project detail, task grouping |
| Epics | Epic list, epic detail |

### Tech

React + Tauri (carried from Engine). HTTP API backend via `torque serve`. SSE for real-time updates.

### Removed from Engine

- Chat entirely
- Provider management (executor plugins own this)
- Agent profile management in core (plugin territory)
- Sync/export UI (may return as plugin)

---

## 10. Approach

**Fork + Rebuild Core (Approach C).**

New repo (`torque`) with only core pieces ported from Engine. Cherry-pick task FSM, runner, persistence, plugin host, MCP adapter. Build executor plugin interface fresh using the best of Engine + Nanite patterns. Old repo stays as reference.

**Rationale:** No deadline, no external users, no migration burden. Build it right, not fast. Saves time long-term by avoiding legacy baggage and tech debt.

### What Gets Ported from Engine

- Task FSM and service layer (adapted for new data model)
- Scheduler core loop (adapted for executor plugin interface)
- SQLStore and migration patterns (new schema, same patterns)
- MCP adapter structure (new tool names, same approach)
- Plugin host (go-plugin SDK integration)
- GUI shell (React/Tauri, stripped down)

### What Gets Built Fresh

- Executor plugin interface and shipped plugins
- Tool router, permission engine, sandbox (from Nanite patterns)
- Deliverable system
- Merge policy and agent-assisted resolution
- Database concurrency model (split writes, go-queue buffer)
- Signal protocol (TORQUE_* replacing VOLON_*)
- All configuration and env vars

### What's Left Behind

- VOLON signal protocol and all VOLON_* references
- Fragments Engine naming and module paths
- Chat system
- Built-in provider management
- Agent identity system (becomes plugin territory)
- Nexus integration in core (becomes plugin territory)
- Legacy proto namespace (volon.tasks.v1)
