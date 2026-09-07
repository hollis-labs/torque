# vNext — plan

**Status: agreed rough cut.** Sequencing settled 2026-09-07; the phases below
are the shape work is created against. Sizes are relative session-sets, not
calendar estimates: **S** ≈ one focused session · **M** ≈ a few · **L** ≈ a
sustained run.

Read [architecture.md](architecture.md) first. Every phase cites the section it
implements.

## Records

Created 2026-09-07 under plan **CW-20260907-0047**.

| Phase | ID | Depends on |
|---|---|---|
| R — permissive FSM | `CW-20260907-0048` | — (independent) |
| 1 — freeze contracts | `CW-20260907-0049` | — |
| 2 — subtractions | `CW-20260907-0050` | P1 |
| 3 — records + importer | `CW-20260907-0051` | P1 |
| 4 — lifecycle + scope | `CW-20260907-0052` | P3 |
| 5 — bindings + grants | `CW-20260907-0053` | P4 |
| 6 — gates | `CW-20260907-0054` | P5 |
| 7 — workflow executor | `CW-20260907-0055` | P6 |
| 8 — vehicle + launch | `CW-20260907-0056` | P7 |
| 9 — plugins + surface + GUI | `CW-20260907-0057` | P8 |
| 10 — cutover + install | `CW-20260907-0058` | P9 |

R runs whenever. P2 and P3 both hang off P1 rather than chaining — subtraction
and the new record model do not touch each other, so they can run in parallel.

---

## The live board, measured

`~/.local/share/torque/workspaces/default/main.db`, read 2026-09-07. These
numbers set the plan's risk, not intuition about them.

| | |
|---|---|
| tasks | **3,050** |
| comments | 3,812 |
| task_dependencies | 1,025 |
| runs / run_events | 1,071 / **23,396** |
| sessions / session_checkpoints | 642 / 61 |
| artifacts | 683 |
| sprints / epics / projects / collections | 250 / 79 / 29 / 4 |
| cost_ledger | 597 |
| checkpoints | 31 |
| **messages / message_deliveries** | **10 / 5** |
| **scheduler_jobs / worker_heartbeats / worktrees** | **0 / 0 / 0** |
| runs started in the last 30 days | **2** |

Three findings change the plan.

**1. The board has already outgrown the FSM.** 168 tasks sit in statuses
`validTransitions` has no key for — `abandoned` (99, most recently written
2026-09-04), `backlog` (61), `cancelled` (8). `Transition` answers
`unknown source status` for every one of them; only `ForceTransition` moves
them. Users needed vocabulary the FSM did not have and took it by writing
statuses the FSM does not know. That is [§3.1](architecture.md#31-work-lifecycle--canonical-permissive-overlaid)
found in the data rather than argued for, and it widens phase R.

**2. Messaging is unused.** Ten messages and five deliveries, against 4,900 LOC
of transport, federation and broker. Phase 2's deletion is confirmed safe rather
than assumed safe.

**3. The scheduler is not running in production.** Zero jobs, zero heartbeats,
zero worktrees, two runs in thirty days. Work is dispatched by hand. This
materially de-risks the parallel-daemon period — the vNext daemon does not have
to preserve live scheduling behaviour while it is being built — and it raises a
question the plan should not dodge: how much of the 12,500-LOC scheduler is
earning its keep, and how much is machinery for a mode of operation that has not
been used?

---

## Settled sequencing decisions

- **Parallel daemon.** vNext builds beside v1 on its own port and store. The
  importer runs repeatedly against a v1 snapshot until the diff is clean. One
  cutover, and the importer is rehearsed rather than trusted. This is the only
  shape where `CW-*` ID preservation is proven before it matters.
- **Gates and the workflow executor move up**, to just after dispatch bindings.
  Fan-out is the weakest thing Torque has today; gates are what the workflow
  host needs for `wait.Materializer`, so they travel together.
- **Plans are parked, not carried.** The prompt-driven plan-walking loop is
  deleted in phase 2 and returns properly in phase 7. Dead-end code carried
  through six phases is worse than a gap.
- **Records are created now**, because the parallel daemon keeps v1 serving the
  board throughout.

## Shape

Three principles drive the order: **freeze contracts before building against
them** · **subtract before rebuilding** · **keep the board usable**.

```text
R ──────────────────────────────────────────────  relief, independent, now
    1 ── 2 ── 3 ── 4 ── 5 ── 6 ── 7 ── 8 ── 9 ── 10
    │    │    │                             │    └── cutover, v1 deleted
    │    │    └── parallel daemon starts    └── GUI leaves
    │    └── ~7k LOC deleted
    └── design only
```

---

## R — Relief: make the FSM permissive now

**Size: S. Before anything else. Independent of everything after it.**

- `validTransitions` becomes permissive — any state to any state except into
  `proposed` and out of `archived`.
- **Handle unknown source statuses.** 168 live rows are currently untransitionable
  through the normal path. A status the map does not know must not be a dead end.
- Verify `internal/runtime/scheduler/picker.go` will not re-dispatch a reopened
  `done` item now that the transition is legal.
- Update `agent-setup`'s stop-disposition hook, which hardcodes
  `todo → doing → review → done` in its user-facing message.
- Closes **CW-20260903-0070**.

Forward-compatible by construction: permissive is the vNext default, and the
overlay that re-adds rigidity arrives in phase 4. This removes daily friction
weeks before the architecture does.

---

## 1 — Freeze the contracts

**Size: M. Design only, no code.**

| Deliverable | Implements |
|---|---|
| ADR — vocabulary, core/plugin boundary, the stock-boundary list | §1.1, §2, §11 |
| ADR — work lifecycle, canonical states, overlay schema, precedence chain | §3.1, §5 |
| ADR — the record model and the work-event / run-telemetry split | §4 |
| Contract draft — `Executor`, `SessionHost`, `LaunchProvider`, `GateSurface`, `ReadinessPredicate`, `LifecycleOverlay` | §2, §6, §7 |
| Import mapping spec — v1 row → v2 records, ID preservation, event seeding, the 168 non-canonical statuses | §12 |
| Plugin manifest spec — what a Torque `plugin.yaml` must declare | §11 |

Also settle here: **the scheduler question**, below. It is a contract question,
and answering it late would waste phase 5.

**Done means:** the six documents exist, the eight open questions in §14 are
answered or explicitly deferred with a reason, and every later phase can name the
contract it implements.

### The scheduler question

**See [execution-vocabulary.md](execution-vocabulary.md) for the full analysis.**
It separates six concerns that three words are currently covering, checks the
naming against Temporal / Airflow / Kubernetes / Nomad / Celery, maps the
`hollis-labs` libraries onto the result, and lands two findings: claim/lease/fence
is implemented three times, and nothing in the shared set evaluates a *mutable*
work graph. The short version follows.

Two different things are wearing one word, and Torque is the last consumer that
has not sorted them out.

| | `go-scheduler` | Torque's `internal/runtime/scheduler` |
|---|---|---|
| What it is | **Timed activation** — what is due now | **Work dispatch** — what is eligible, in what order |
| Owns | schedule rows, durable fire identity, CAS claim, lease + fencing, retry/backoff, exhaustion, observer events, restart recovery | readiness, priority, concurrency, precheck, lifecycle application, worker verification, end-agents, heartbeat, cost, escalation, parent rollup, cancellation |
| Size | **949 LOC** (v0.2.0, 2026-09-04) | **4,828 LOC** non-test (12,485 with tests) |

**The rest of the portfolio has already moved.**

- **Nanite** runs `go-scheduler v0.2.0`. Its `internal/scheduler/` is **1,269
  LOC of adapters only** — `store_adapter`, `runner_adapter`, `telemetry`. The
  migration was a documented *full replace, not dual-run*: it deleted a
  two-minute ticker and a lookback heuristic outright.
- **Hadron** runs `go-scheduler v0.1.1` behind **485 LOC** of adapters, and
  drives workflow activations through it.
- **Torque runs none of it.** `scheduler_jobs` is a queue-job join table, not a
  schedule table; Torque has no timed-activation concept at all, which is what
  **CW-20260904-0163** was opened to add.

Four questions, in the order they should be answered.

**1. Can the shared library replace part of ours?** The overlap is real and it
is the part hardest to get right: durable claim, lease, fencing token, retry
classification, restart recovery. `go-scheduler v0.2.0` has all of it under CAS
with concurrent-dispatch tests, and Torque hand-rolled the same shapes. Nanite's
store adapter over its own schema is the existence proof that this works without
surrendering the app's persistence model.

**2. Does the shared library need to grow?** It dispatches *opaque jobs* from
*due schedules*. It has no notion of readiness predicates, priority, fairness or
per-executor concurrency — and its own README says applications keep "job types,
activation policy, persistence schemas, and execution." Torque's picker **is**
activation policy. The likely answer is that the library does not need to grow
and the seam is already in the right place: the library fires, Torque decides.
If Torque finds a genuine gap in claim/lease/fence semantics, that is a change
worth making upstream — where Nanite and Hadron get it too.

**3. Do we need it at all?** Production has run 0 scheduler_jobs, 0 heartbeats,
0 worktrees and 2 runs in thirty days. Torque today is used as a **board**, not
a **dispatcher**. Timed activation may be a capability nobody has asked for.

**4. The question the data forces.** 4,828 LOC of dispatch machinery has never
been load-bearing in production. Faithfully porting it into vNext would be
preserving something unproven at considerable cost. The phase-1 job is not "how
do we move the scheduler" — it is **what should dispatch be, for the use we
actually want**, and then how much of that the shared library already provides.

Absorbs **CW-20260904-0163**.

The deeper seam this exposed — that *time-driven* and *policy-driven* are not
two systems but **activation** versus **evaluation**, both feeding one queue /
assignment / worker path — is in
[execution-vocabulary.md](execution-vocabulary.md). Phase 1 should settle the
vocabulary before phase 5 builds on it, because `internal/runtime/scheduler`
currently spans five of the six concerns under one name.

---

## 2 — Subtractions

**Size: M. Pure deletion. No new abstractions.**

- `internal/federation` (1,836), `internal/broker` (809), `internal/messaging`
  transport (2,329) — confirmed unused by finding 2. Retain ~200 lines as the
  `Notifier` / `Steerer` contract stubs. §8
- `internal/planner` (499), `internal/planstart` (1,211),
  `internal/orchestrator` (163) — the prompt-driven plan-walking loop. §3.3
- `internal/launchprofile` (752), `internal/agentfile` (218) as a user-facing
  catalog; register the internal-agents set narrowly. §6.3
- Retire or relocate `docs/superpowers/` and the historical `docs/architecture/`
  tree before the repo goes public.

**Done means:** ~7,000 LOC gone, `make test` green, the board still works,
nothing new introduced.

---

## 3 — Records, the event log, and the importer

**Size: L. The spine. The parallel daemon starts here.**

Parallel-daemon mechanics, so the period is manageable rather than a swamp:

- `cmd/torqued` is the vNext daemon. `cmd/torque serve` stays as v1 until
  phase 10.
- New domain packages sit beside the old ones, named for the model —
  `internal/work/`, `internal/scope/`, `internal/dispatch/`,
  `internal/lifecycle/` — never `v2/`, which rots into permanence.
- Shared mechanics (`runtime/agent`, `go-sqlite` wiring, worktrees) are consumed
  by both until the phase that reworks them.
- Separate port, separate store path. v1 is untouched and keeps serving the
  board.

The work itself:

- Greenfield schema restarting at `001`. Eight tables. §4, §12
- `work_events` append-only; `work_items` / `work_edges` as projections.
- One commit boundary for transition + attempt creation + dispatch intent +
  event emission; transactional outbox where a separate queue is unavoidable.
- The queue moves into the primary store; `go-queue` retained for telemetry.
- `run_telemetry` split from work events — allowlisted projection, bounded
  retention, source events never persisted. §4.1
- **The importer**: re-runnable against a v1 snapshot, diffable output, `CW-*`
  IDs preserved, the 168 non-canonical statuses mapped explicitly. 23,396
  `run_events` are not imported.
- Preserve verbatim: the `go-sqlite` writer/reader split and the concurrency
  pattern.

**Done means:** the live board round-trips through the importer with identical
IDs, the diff is reviewed by a human, and `torqued` serves every read v1 does.

---

## 4 — Lifecycle, scope, and configuration

**Size: M.** §3.1, §5

- `Scope` as a core entity — id, parent, name, config. Nested, cascading.
- The scope config object; `.torque/scope.yaml` authored, explicit adopt.
- The precedence-chain compiler, narrow-only **enforced**, not documented.
- The overlay engine: display vocabulary, sub-states, `require`, `forbid`,
  kind/tag scoping. `backlog` becomes a legitimate declared sub-state rather
  than drift.
- **Rejections name the rule** and say what would satisfy it.
- `ForceTransition` becomes a recorded override carrying actor and reason.
- Readiness as a maintained field with reasons.
- `dispatch_policy` replaces the `manual` boolean.

**Done means:** a scope can rename every state and add a required gate, and
Torque's machinery is provably blind to the vocabulary.

---

## 5 — Dispatch bindings, effect grants, attempts

**Size: M.** §4.3, §4.4, §3.2 · closes **CW-20260904-0166**, **CW-20260904-0090**

- `ExecutionPolicy` — authored, versioned, separately authorized.
- `DispatchBinding` — immutable, one per attempt, with digests.
- The effect-grant compiler over the precedence chain; one grant set per binding.
- Attempt lifecycle separated from work status; claims, leases, fencing.
- The loopback capability token: closure-bound, role-scoped, expiring.
- Gate *records* land here; the gate *semantics* land in phase 6.
- Whatever phase 1 decided about the scheduler's real shape.

**Done means:** "why did this run the way it did?" is answerable from one row,
and secrets are provably absent from the persisted binding.

---

## 6 — Gates

**Size: M.** §7

- Idempotent open on `(caller_scope, idempotency_key)`.
- Resumable completion — inline within N seconds, then a **successful pending
  receipt with a durable handle**; never an error, never a cancellation.
- Authority from an authenticated principal against an eligible-responder
  policy; caller-supplied names are provenance only.
- Split `gates` from `attempt_checkpoints`.
- Keep: reject a required policy the substrate cannot enforce deterministically.
- `GateSurface` contract; `gate.local` default over HTTP + MCP + SSE.

**Done means:** a gate survives a restart, a caller timeout and a transport drop
without the decision being lost or duplicated. Phase 7 has its
`wait.Materializer`.

---

## 7 — Workflow executor on go-workflow

**Size: L.** §3.3

- Torque as a `go-workflow` host: `runtime.StateStore` over `work_events`,
  `wait.ActivationScheduler` over `go-scheduler` v0.2.0 (**CW-20260904-0163**),
  `wait.Materializer` + `ResponderAuthorizer` over phase 6, `values.ArtifactStore`
  over evidence, a frozen `stepkind.Registry`.
- Register `torque.work@v1` and `torque.gate@v1`.
- Enable the adapters worth having: `http`, `llm`, `mcp`, `script`, `cmd`,
  `transform`, `call`.
- Plans return as graphs. `for_each` over `torque.work@v1` gives durable,
  resumable, concurrency-capped fan-out with a tolerated-failure policy.
- Conformance: `RunRequired` minimum; `RunComplete` if memoization or
  verification is enabled.

**Done means:** the machinery deleted in phase 2 is back and better, with no
agent walking phases by being prompted to.

---

## 8 — Vehicle and launch providers

**Size: M.** §6

- Split `internal/runtime/agent` into `runtime/session` (vehicle) and
  `executor/agent` (actor adapter). Clean up, do not exile.
- `LaunchProvider` registry; `launch.projectdir` as the built-in default — the
  repo's own `AGENTS.md` is the agent.
- The Cairn-shaped planter on `agentkit/agentruntime/bootdir` + `agentcontext`:
  templates with slot markers, trees copied whole, files, provider-keyed
  settings **owned by key not by file**.
- The `ExecutionContract`, the `RUN CONTEXT: runtime-managed` marker, the boot
  report and its digest into the binding.
- Preserve verbatim: `TORQUE_WORK_ROOT`/`TORQUE_REPO_ROOT`, sibling-depth
  worktrees, the relative-`replace` guard, the git precheck, the base-ref chain,
  the non-interactive permission mode.

**Done means:** a fresh clone with an `AGENTS.md` and no configuration dispatches
an agent, and Torque authors no line of that agent's identity.

---

## 9 — Plugin scaffold, surface cut, GUI extraction

**Size: L.** §10, §11

- `plugin-sdk` host: `internal/plugin/builtin/<id>/` with an embedded
  `plugin.yaml` and `init()` registration; `allplugins` blank-imported by
  `cmd/torqued`. Nanite's mechanics.
- Two tiers — compiled-in hot path, subprocess cold path, one manifest.
- **The stock-boundary assertion test.** Hadron's discipline.
- Move out to `builtin/`: project, sprint, epic, collection, template,
  importers, cost, audit.
- MCP cut to ~30 core tools plus plugin-contributed families;
  `notifications/tools/list_changed` replaces restart-to-see-flags.
- HTTP API versioned as a real contract.
- **GUI extracted to its own repository** against that now-settled API.

**Done means:** `torqued` with zero plugins loaded still coordinates and
dispatches; the stock boundary test names what a fresh install does; the GUI
builds out of tree.

The GUI stays embedded through phases 3–8 deliberately. It is the daily board and
the API moves until here. One catch-up, not eight.

---

## 10 — Cutover, install story, public repo

**Size: M.**

- Final import run; human-reviewed diff; `torqued` becomes `torque`.
- **v1 deleted** — `cmd/torque serve`, the old schema, the superseded packages.
- `brew` tap and `go install`; one binary that works.
- `getting-started.md` rewritten against the real thing; docs sweep.
- The stock boundary documented, not only asserted.
- License, `SECURITY.md`, `CONTRIBUTING.md`, public README.

**Done means:** a developer who has never seen Torque installs it and dispatches
a task against their own repo without reading Go.

---

## Gated on other work

Runs in parallel; lands when the upstream contract exists. None of it blocks
R–10.

| Item | Gated on | Record |
|---|---|---|
| `host.tether` — remote session host | Tether launch hosting | CW-20260904-0102 |
| `launch.tether` — Tether as launch provider | same | — |
| `notify.tether` / `steer.tether` | Tether messaging cutover | CW-20260904-0103 |
| `go-app-agent` adoption | Tangent releasing it | CW-20260906-0091 |
| `executor.a2a` | A2A adapter stabilizing | CW-20260904-0106 |

## Superseded

**CW-20260904-0117** — "Planning brief — Torque standalone and composed agent
execution", currently `doing`. Its direction is compatible and absorbed; it
should be dispositioned rather than left open beside this plan.
