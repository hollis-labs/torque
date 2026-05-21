# ADR-0003 — Harness-emitted Coordination Events

**Status:** Proposed
**Date:** 2026-05-20
**Task:** CW-20260519-0126 (ph-2 of CW-20260519-0134)
**Supersedes:** —

---

## Context

When durable agents coordinate (Torque Supervisor, Project Manager,
Architect/Planner, PKM, Release Manager, plus the existing orchestrator
template), many signals can be emitted by the **harness** on the agent's
behalf instead of asking the agent to draft a comment/message every turn.
The agent then **reacts to canonical events** rather than polling state.

Two adjacent surfaces already exist in Torque:

- `internal/runtime/scheduler.EventBus` — in-process fan-out, drop-on-full,
  256-buffer per subscriber. Today's emissions: `task.transitioned`,
  `task.notify`, `task.created` (SSE-direct), `run.started`, `run.event`,
  `run.progress` (heartbeat/artifact/tool_use/output), `run.completed`,
  `run.finished`, `run.canceled`, `run.superseded`, `worker.stale`,
  `wait.fired`, `scheduler.tick`, `checkpoint.timed_out`.
- `internal/broker` (mux envelope broker, federated) — `notice`,
  `status_update`, `handoff`, `escalation`, `request`, `response`, plus
  per-envelope `envelope.sent` / `envelope.delivered` SSE.

A scoped audit of the sibling Nanite codebase (separate repo; sibling
checkout under `apps/nanite/` in development setups)
found that Nanite's event surface is **mature for intra-app observability**
(plugin events, session_events table, store events) but **not designed for
cross-host coordination** — there is no URN scheme, no mux bridge, and the
coordination layer (`internal/coordination/`) is KV-only with no event
emission. Implication: Torque does not *converge onto* a Nanite shape —
**Torque defines the cross-host canonical envelope, and Nanite becomes a
future consumer via the existing mux envelope broker.**

The orchestrator stand-down on 2026-05-19 was partly caused by *no event
told the orchestrator that ph-1 had been removed*. Plan structural changes
(phase add/remove) are the highest-priority gap to close.

## Decision

**Two layers, with a clear split:**

### Layer 1 — Internal `scheduler.EventBus` (already exists)

The in-process fan-out for everything the daemon and its in-process
consumers (SSE bridge, session-lifecycle-hook, future supervisors) need to
react to. Stays exactly as it is today. We **add new event kinds** to
close the gaps below, but the `SchedulerEvent` shape (`Type`, `TaskID`,
`RunID`, `Data interface{}`, `Timestamp`) does not change — additive only.

### Layer 2 — Canonical cross-host envelope (new, lives next to bridge)

For events that should be visible to cross-host consumers (durable agents
running under Nanite or another host, future federation peers), we define
a canonical envelope wrapping the Layer-1 event with the additional
fields a cross-host consumer needs to disambiguate sources.

```go
// internal/runtime/scheduler/canonical_event.go
type CanonicalEvent struct {
    SchemaVersion int                    `json:"schema_version"`
    EventID       string                 `json:"id"`        // "evt_" + 24-char random hex (12 bytes)
    Kind          string                 `json:"kind"`      // e.g. "task.transitioned"
    Source        string                 `json:"source"`    // "torque"
    URN           string                 `json:"urn"`       // urn:torque:<entity>:<id>
    TaskID        string                 `json:"task_id,omitempty"`
    RunID         int64                  `json:"run_id,omitempty"`
    OccurredAt    time.Time              `json:"occurred_at"`
    Actor         CanonicalActor         `json:"actor"`     // {kind, id}
    Payload       map[string]interface{} `json:"payload,omitempty"`
}
```

The translation `SchedulerEvent → CanonicalEvent` lives in
`canonical_event.go`. Today the only consumer is a sketch; **wiring the
canonical envelope onto the mux envelope broker is deliberately deferred**
to a follow-on task. This ADR defines the shape; the bridge wiring is
gated on (a) at least one external consumer landing, (b) a decision on
event filtering — not every Layer-1 emission should cross hosts (e.g.
`run.progress {kind:heartbeat}`, `scheduler.tick`, `run.event` are
high-frequency and would flood mux).

### URN scheme

`urn:<host>:<entity>:<id>` — e.g.

| URN | Meaning |
|---|---|
| `urn:torque:task:CW-20260519-0126` | A task record |
| `urn:torque:run:42` | A run by integer RunID |
| `urn:torque:session:<UUID>` | An agent session |
| `urn:torque:plan:CW-...` | A kind=plan task (same as task URN; ambiguity intentional, callers know the kind from `kind:` of the event) |
| `urn:torque:daemon:<pid>` | The serve process itself (PID known) |
| `urn:torque:daemon:default` | Daemon-scoped event with no PID in payload (e.g. `scheduler.tick`) |
| `urn:torque:daemon:unknown` | `URNForDaemon(pid)` called with pid ≤ 0 |
| `urn:nanite:session:<UUID>` | (Future) a Nanite session |

Host segment ASCII-lowercase, alphanumeric + `-`. Entity segment is one of
`task | run | session | plan | daemon | worker | phase` for now; extend
additively. URN is opaque to consumers — string-equality + prefix-match
only.

### Event kinds (current + new)

**Already emitted (Layer 1):**

| Kind | Source | Trigger |
|---|---|---|
| `task.transitioned` | scheduler / lifecycle | FSM transition (scheduler-driven) |
| `task.notify` | lifecycle / waitpoll / parent_rollup | notification reason (free-form) |
| `task.created` | (SSE-direct in httpserver) | new task persisted |
| `plan.created` | (SSE-direct in httpserver) | new plan persisted |
| `run.started` | scheduler | dispatch to executor |
| `run.event` | scheduler | passthrough of exec stream event |
| `run.progress` | scheduler / progress_heartbeat | heartbeat/artifact/tool_use/output |
| `run.completed` | scheduler | run finished with status + cost |
| `run.finished` | lifecycle | post-completion bookkeeping |
| `run.canceled` | scheduler | external cancel |
| `run.superseded` | lifecycle | late completion after replacement |
| `worker.stale` | scheduler | heartbeat staleness |
| `wait.fired` | waitpoll_dispatch | wait predicate fired |
| `scheduler.tick` | scheduler | per-tick boundary |
| `checkpoint.timed_out` | checkpoint sweeper | HITL checkpoint timeout |

**New (Phase 3 — priority emissions):**

| Kind | Trigger | Payload |
|---|---|---|
| `plan.phase_added` | `PlanService.AddPhase` | `{phase_id, phase_name, order}` |
| `plan.phase_removed` | `PlanService.RemovePhase` | `{phase_id}` |
| `daemon.up` | serve.go startup | `{pid, started_at}` + optional `{binary_path, binary_mtime}` (best-effort; populated when `os.Executable` + `os.Stat` succeed) |
| `scheduler.dispatch_skipped` | per-tick, after picker (suppressed when no skips) | `{tick, candidates, picked, counts:{<reason>:<n>}}` |
| `scheduler.toggled` | settings update of `scheduler.enabled` | `{enabled: bool, by}` |

**Deferred (Phase 4+):**

- `daemon.down` — needs a flush guarantee (bus dies before subscriber sees
  it on normal shutdown). Tracked separately; likely best surfaced via the
  envelope broker as an `escalation` from a watchdog.
- `task.created` and `plan.created` on the bus (currently SSE-direct in
  httpserver) — small refactor to route through the bus consistently;
  defer to keep this change surgical.
- Mux envelope-broker bridge wiring for the canonical envelope.

### Why these five for Phase 3

1. **`plan.phase_added`** — the directly named gap from the 2026-05-19
   orchestrator stand-down. The orchestrator (and future Project Manager
   / Architect agents) need to react to plan structural change without
   polling `torque_plan_get`.
2. **`plan.phase_removed`** — symmetric to `plan.phase_added`; the
   stand-down failure mode was specifically *no event told the
   orchestrator that ph-1 had been removed*, so removal is the
   higher-priority half of the pair.
3. **`daemon.up`** — durable agents need to know when the harness
   restarted so they can re-fetch state and clear stale assumptions. PID
   + binary mtime is enough to distinguish "same process" from
   "redeployed binary".
4. **`scheduler.dispatch_skipped`** — operators today read the per-tick
   `[picker] tick=N candidates=C picked=P skipped_by_reason=map[...]` log
   line to answer "why isn't anything dispatching?" Surfacing the same
   counts onto the bus makes the GUI / supervisor surfaces queryable
   without log-scraping.
5. **`scheduler.toggled`** — `torque_scheduler_toggle` exists; emitting
   on toggle lets supervisors react to the operator pause-button.

### Observer/emitter wiring for service-layer emissions

`PlanService` lives in `internal/service` and does not depend on
`internal/runtime/scheduler`. We avoid the import direction by using the
**observer pattern already established for `TaskTransitionObserver` and
`CommentObserver`**: introduce a `PlanPhaseObserver` interface in
`internal/service` and wire it from the daemon to a small adapter that
publishes to the EventBus. This keeps `service` import-clean and matches
the precedent.

## Consequences

- **Non-breaking.** `SchedulerEvent` shape unchanged; new kinds are
  additive. SSE bridge forwards them automatically.
- **The canonical envelope ships as a type definition, not a wire.** No
  mux federation behavior changes in this PR. The follow-up to wire the
  bridge happens once we have the first external consumer to validate
  the shape against.
- **High-frequency events stay Layer-1 only.** No new mux traffic from
  this change. When the bridge lands, an event-filter table will decide
  which kinds cross hosts.
- **The orchestrator stand-down failure mode is closed** for the plan
  structural surface — agents subscribing to `plan.phase_added` /
  `plan.phase_removed` get a push signal instead of needing to poll.
- **Cross-host parity with Nanite is *future-shaped*, not current.**
  Nanite has no symmetric emission today; when it grows one, it adopts
  the canonical envelope shape this ADR defines.

## Related

- ADR-0001 — Torque Messaging Design Lock (envelope-broker `kind` taxonomy)
- ADR-0002 — Federation Hop Auth + Trust
- Plan CW-20260519-0134 — Durable-agent runtime
- Sibling doc — `agent-coordination-patterns-2026-05-19.md` (agent-side
  patterns; this ADR is the harness-side foundation those patterns
  depend on)
