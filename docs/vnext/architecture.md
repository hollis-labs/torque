# Torque vNext — architecture sketch

**Status: exploration.** Read [context.md](context.md) first; it records what
this was built from and the eight session rulings that constrain it.

## 1. Identity

> Torque decides what work runs, binds it to an immutable execution request,
> dispatches it, and governs the outcome. Everything that does the work is an
> executor.

Torque is a local-first durable **work-coordination and dispatch control
plane**. It owns the canonical record of deliberately-managed work, evaluates
what is eligible to run, produces an explainable binding, hands that binding to
a registered executor, and applies the normalized result against the work
item's acceptance policy.

It does not run agents, define workflows, host conversations, store artifact
content, manage secrets, or provision infrastructure. It dispatches to things
that do, and it composes with them through references, handles, typed commands,
events and evidence records.

The one-line test: **if it is not about deciding, binding, dispatching or
governing work, it is a plugin.**

### 1.1 Vocabulary

Four words that were previously one, because collapsing them pushed the wrong
things out of core.

| Term | Meaning | Whose |
|---|---|---|
| **Actor** | Who or what performs the work — an agent, a workflow, a person, an external system, a process | — |
| **Executor** | The adapter binding a `DispatchBinding` to an actor and returning a normalized result | Core contract; core ships `agent`, `human`, `external` |
| **Vehicle** (`SessionHost`) | How Torque gets and supervises a *process* for an actor that needs one | **Core.** `agentkit` + `go-agent-wrapper` |
| **LaunchProvider** | Resolves *which agent* into something the vehicle can boot | Core contract; core ships `projectdir` |
| **Agent** | Identity, role, prose, skills, composition | **Never Torque's** |

The vehicle is the agent's workspace: Torque builds it, supervises it, tears it
down, and recovers it. Torque does not decide who moves in. Owning the workspace
is dispatch mechanics and belongs in core; owning the occupant is agent
authoring and does not.

## 2. The core / plugin line

### Core

| Concern | What core owns |
|---|---|
| Scope | The ownership / policy / visibility boundary. Nested. Config attaches here. |
| Work graph | `WorkItem` identity, kind, brief, provenance, revision; typed `WorkEdge` relations |
| Work lifecycle | The canonical FSM plus the scope-overlay engine |
| Readiness | Derived eligibility, with reasons, as a first-class queryable fact |
| Scheduler | Priority, fairness, concurrency, claims, leases, fencing, heartbeat, recovery |
| Dispatch binding | The immutable per-attempt snapshot |
| Attempt lifecycle | The fixed, non-configurable execution state machine |
| Executor registry | **One** contract, capability declaration, health, cancellation |
| Vehicle | The embedded session host on `agentkit` + `go-agent-wrapper`: boot-dir materialization, process lifecycle, turn / steer / cancel / resume, event normalization, worktrees, permission mode, recovery |
| `executor.agent` | The agent actor adapter, over the core vehicle — standalone Torque dispatches to an agent with zero plugins loaded |
| Launch provider registry | The `ExecutionContract` → `PreparedLaunch` seam, plus the `projectdir` default |
| Gates | Human decisions and external conditions that govern work |
| Evidence | Locators and digests, never content |
| Work event log | Append-only; the source of truth that every projection is built from |
| Effect grants | One compiler over the precedence chain, one immutable grant set per binding |
| Config | The precedence chain and the scope config object |
| Storage | One store, SQLite or Postgres, one commit boundary |
| Surfaces | HTTP, MCP, CLI over the same domain services; SSE |

### Plugin

Everything else, including things that feel core today:

- **`launch.cairn`**, **`launch.tether`**, **`launch.nanite`** — launch providers
  beyond the built-in `projectdir` default. These say *who the agent is*; none of
  them is required for Torque to dispatch to an agent.
- **`host.tether`** — a remote session host, for when Tether owns the process
  rather than Torque. The `SessionHost` contract is core; the embedded
  implementation is core; alternative hosts are plugins.
- **`executor.workflow`** — a go-workflow / Hadron graph as a work item's
  executor. Absorbs today's planner / orchestrator / plan-phase machinery.
- **`executor.a2a`**, **`executor.command`**, **`executor.external`**.
- **`project`** — repo path, agent path, read/write/context paths, rules,
  artifacts. A decoration over a Scope, not a competing concept.
- **`sprint` / `epic` / `collection`** — groupings within a scope.
- **`template`** — authored work templates and instantiation.
- **`gate.local`** (default) and **`gate.tangent`** — gate surfaces.
- **`notify.*` / `steer.*`** — how a gate reaches a human, how a message reaches
  a running attempt.
- **`import.*`** — GitHub issues and other sources.
- **`evidence.*`** — deliverable verifiers.
- **`cost.modelsdev`**, **`audit.*`**, **`ui.*`**.

### Why Scope is core and Project is not

The scope config object is where the lifecycle overlay, dispatch defaults,
launch selection and effect ceilings live. That cannot depend on a plugin being
loaded. So the boundary is:

- **Scope** — core, minimal: `id`, `parent_id`, `name`, `config`. Every work
  item belongs to exactly one. A deployment has a root scope. Scopes nest and
  config cascades.
- **Project** — a plugin that decorates a scope with repo-shaped facts. It is
  where `repo_path`, `agent_path`, `read_paths`, `rules` and project artifacts
  live today, and they stay there.

## 3. The two lifecycles

Rigid required phases are the friction this whole exercise started from. The
fix is not a looser fixed FSM — it is separating the lifecycle the *user* owns
from the lifecycle *Torque* owns, and letting the user own theirs completely.

### 3.1 Work lifecycle — canonical, permissive, overlaid

Canonical states are a closed set in core:

```text
proposed → open → active → review → done
                ↘ blocked ↙
                ↘ abandoned ↙
done | abandoned → archived
```

**The default rule is permissive.** Any state may move to any other except into
`proposed` (creation only) and out of `archived` (unarchive is a distinct
operation with its own authority). `open → done` is legal. `blocked → active`
is legal. `done → open` is legal. Torque ships with **zero required paths**.

Rigidity is then something a scope *opts into*, and describes in its own words:

```yaml
lifecycle:
  enforce: true

  # The vocabulary is yours. The canonical state is Torque's.
  states:
    backlog:  { canonical: open }
    building: { canonical: active }
    in-qa:    { canonical: review }
    shipped:  { canonical: done }
    parked:   { canonical: blocked }

  require:
    - { to: shipped, via: [in-qa] }
    - { to: shipped, gate: pr_review, when: { kind: agent } }
    - { to: building, when: { tag: needs-design }, gate: design_signoff }

  forbid:
    - { from: shipped, to: building }   # reopening goes back through backlog
```

Rules:

- **Machinery reads the canonical state, never the display name.** The
  scheduler, readiness evaluator and executors are blind to `in-qa`; they see
  `review`. A scope can rename everything without touching a line of Torque.
- **Sub-states map onto exactly one canonical state.** A scope may declare as
  many as it wants; they are vocabulary, not new semantics.
- **Overlays narrow, never widen.** A child scope may add a requirement its
  parent did not have; it may not remove one the parent imposed.
- **A rejected transition names the rule.** Not `transition not permitted` but
  *which* overlay rule rejected it, and what would satisfy it. This is the
  single largest source of agent and human friction today and it is a message
  problem as much as a policy problem.
- **`ForceTransition` survives, but stops being silent.** It becomes an
  authorized operation that writes an override work event carrying an actor and
  a reason. Bypassing the overlay is a recorded act, not an invisible one.

### 3.2 Attempt lifecycle — fixed, not configurable

```text
planned → queued → leased → starting → running → waiting
              ↘ succeeded | failed | timed_out | canceled | lost | superseded
```

This is Torque's own machinery and users never configure it. It carries the
claim, the lease, the fencing token, the heartbeat and the recovery decision.

`succeeded` means the executor fulfilled its invocation contract. **It is
evidence, not acceptance.** A run can succeed and send the work item to review.
A work item can be completed with no run at all. A canceled or superseded run
must never overwrite a later operator decision.

### 3.3 On using go-workflow for this

**Not for these two lifecycles** — but for a reason narrower than it first
looks. The attempt machine is eleven fixed states with no branching, no typed
values and no user authoring; the work FSM is six states with a declarative
overlay. Neither needs a compiler, a plan digest or a value store. Hardcode
both.

That is not a statement about `go-workflow`'s reach, which is considerably
wider than an earlier draft of this document implied. Verified in-tree:

| Capability | Where |
|---|---|
| Fan-out | `graph.ForEachSpec` — `Items`, `MaxConcurrency`, `FailFast`, `ToleratedFailurePolicy` (count *or* percentage); `runtime/fanout.go` (616 LOC) carries durable item bindings, leases and an aggregate lifecycle independent of worker leases |
| Branching | `Node.If`, `graph.SwitchSpec` with ordered arms and a default |
| Joins | `ReadyRule`: `all_success`, `all_done`, `one_failed`, `all_failed`, `none_failed`, `always` |
| Sub-workflows | `Node.Call`, plus Hadron's pinned child-run materializer |
| Error handling | `CatchRule` (typed selectors, `when` narrowing, `bind_as`), `FinallySpec`, `CompensationSpec` |
| Everything else | retry + backoff, idempotency, five-way timeouts (queue / execution / wait / heartbeat / schedule-to-close), concurrency claims, memoization, verification, durability, effect classification, policy requirements, execution-target requirements |

And the adapter set already covers the n8n-class kit: `http`, `llm`, `mcp`,
`agent`, `script`, `cmd`, `transform`, `service`, `gate`, `wait`, `emit`,
`checkpoint`, `call`, plus generated API and child adapters.

Hadron's stock daemon exposing only six frozen kinds is a **host** choice, not
an engine limit — the library's own adoption guide says so in those words. A
host registers the kinds it wants and freezes the registry for a plan's
lifetime.

So `go-workflow` belongs one level out, as the substrate for **`executor.workflow`**,
which absorbs today's plan / phase / planner / orchestrator machinery. Torque
becomes a `go-workflow` host and supplies the seams the library expects:
`runtime.StateStore` (over `work_events` — the engine wants durable CAS,
idempotency and an append-only event log, which is exactly §4.1),
`wait.ActivationScheduler` (over `go-scheduler`), `wait.Materializer` +
`ResponderAuthorizer` (over §7 gates), `values.ArtifactStore` (over §4.5
evidence), and a frozen `stepkind.Registry`.

The kind that makes this Torque's rather than a second Hadron:

```text
torque.work@v1     a node dispatches a Torque work item and joins on its outcome
torque.gate@v1     a node opens a Torque gate and waits on an authenticated decision
```

With `torque.work@v1` under a `for_each`, a plan phase that says "review every
changed package" becomes a fan-out over N child work items with a concurrency
cap and a tolerated-failure policy — durable, resumable and recoverable, with
no plan-walking prompt loop. That is the honest fix for the current plan
machinery, which walks phases by prompting an orchestrator agent.

Conformance target: `RunRequired` at minimum, `RunComplete` if memoization or
verification is enabled.

## 4. Records

Eight tables replace the one 49-column row.

### 4.1 `work_events` — append-only, the source of truth

```text
id (monotonic)   scope_id   work_id   seq
type             actor_principal
causation_id     correlation_id
payload          occurred_at   recorded_at
```

Every state-affecting fact. Never pruned. Current state is a projection over
it. This gives complete work history — the direction doc's "current-state rows
lack complete work history" tension — and makes external observers able to
follow reliably instead of polling for diffs.

**Work events are not run telemetry.** Today `run_events` mixes a lifecycle
transition with a provider stream chunk. Split them:

| | `work_events` | `run_telemetry` |
|---|---|---|
| Volume | Low | High |
| Retention | Forever | Bounded |
| Authority | Source of truth | Observation |
| Content | Facts about work | Metadata + explicitly-safe per-kind state |

**From Nanite: the source event is never persisted.** Prompts, tool payloads,
stdout, stderr and model deltas may contain secrets. `run_telemetry` stores an
allowlisted projection with its own cursor, retention, gap and redaction
semantics — never the raw event.

### 4.2 `work_items` — projection

```text
id  scope_id  kind  title  description  dispatch_brief
lifecycle_state  display_state
dispatch_policy  priority  revision
policy_ref  policy_overlay
ready  not_ready_reasons
source_type  source_ref
created_by  created_at  updated_at  archived_at
```

About eighteen columns, from forty-nine. Notes:

- **`description` is data. `dispatch_brief` is authored.** This is the split the
  boot-prompt field was reaching for. An imported or untrusted work item's
  description is shown to an executor; it never becomes the brief the agent is
  booted with. An importer may write `description` and never `dispatch_brief`.
- **`dispatch_policy`** replaces the overloaded `manual` boolean:
  `manual | automatic | externally_activated | never`. Human work, external
  work, containers, decision gates and paused automatic work stop depending on
  one inverted flag to avoid running.
- **`ready` + `not_ready_reasons`** are maintained by the readiness evaluator so
  "why isn't this running?" is a field, not an inference from scheduler skip
  logs. `open` and `ready` are not synonyms and the schema should stop implying
  they are.
- **`display_state`** is the scope's vocabulary. `lifecycle_state` is canonical.

### 4.3 `execution_policies` — authored, versioned, separately authorized

```text
id  scope_id  name  revision  digest
executor_id  launch_ref
tools  effects  environment  permissions
budgets{cost, tokens, duration}
retry{max, classes, backoff}
hooks{on_done, on_fail, on_review, on_done_merge}
authored_by  authored_at
```

A work item **references** a policy and may carry a bounded `policy_overlay`
that **narrows** it. Editing a work brief needs work-edit authority; authoring
or revising an execution policy needs publisher authority. This is the concrete
fix for "an untrusted task description is not a system prompt, tool grant,
environment grant, or permission policy."

### 4.4 `dispatch_bindings` — immutable, one per attempt

```text
attempt_id
work_id + work_revision
readiness_snapshot
resolved_policy (inlined, non-secret) + policy_revision + digest
executor_id + executor_version
launch_provider_id + resolved_launch_digest + boot_report_digest
effect_grants
execution_target  (+ workspace handle / lease when composed)
work_root + repo_root + base_ref
credential_references   (references only, never values)
caller  actor  causation_id  correlation_id
created_at
```

Changing a work item while an attempt runs does not change that attempt. A
retry gets a fresh binding from the current revision. Secrets resolve only at
the narrow execution boundary and are never part of the persisted document.

This directly implements CW-20260904-0166 and makes "why did this run the way
it did?" answerable from one row.

### 4.5 The rest

| Table | Replaces |
|---|---|
| `work_edges` | `depends_on`, `parent_id`, and the implicit sprint/epic/collection links — typed, addressable relations with provenance and lifecycle |
| `attempts` | `runs` plus the run-state columns squatting on `tasks` |
| `gates` | `checkpoints` plus `metadata.hitl` |
| `evidence` | `artifacts`, narrowed to locators and digests |
| `effect_grants` | the compiled result of `tools` + `permissions` + policy + scope + target |
| `deliverables`, `quality_gates`, `checklist_items`, `phases` | four JSON blobs promoted to tables, because the scheduler needs to query them and today one of them is modeled and never read |

`metadata` survives as a declared extension point: a plugin claims a namespace
and declares its shape. It stops being the place required policy hides.

## 5. Configuration

One precedence chain, one compiler, one explainable answer:

```text
operator / deployment policy
  → scope config          (nested scopes cascade)
    → execution policy    (authored)
      → work item overlay
        → attempt override
```

**Later layers narrow only.** A layer can never elevate permissions, budgets,
network access or credential scope beyond its parent — enforced, not documented.
A launch profile cannot grant itself `bypassPermissions` when the scope forbids
it.

The scope config object:

```yaml
scope: hollis-labs/torque

lifecycle:      # §3.1
  enforce: false

dispatch:
  default_policy: manual
  concurrency: { max_active: 3, max_per_executor: { agent.cli: 2 } }

launch:
  provider: projectdir
  ref: "."                    # the repo's own AGENTS.md is the agent

executors:
  allow: [agent.cli, workflow, command]

effects:
  ceiling:
    network: deny
    filesystem: { write: ["$WORK_ROOT"] }

gates:
  surface: local              # or tangent
  defaults: { deadline: 24h, on_timeout: block }

retention:
  run_telemetry: 30d
```

**Authoring vs. authority.** The file (`.torque/scope.yaml`) is where a scope's
config is *authored* and can live in git alongside `AGENTS.md`. The database
holds the *adopted* revision. Adoption is an explicit operation, so cloning an
untrusted repo cannot change dispatch policy, effect ceilings or executor
allowlists by existing. Same discipline as everything else in this sketch:
content is data until an authorized actor adopts it.

## 6. Launch and materialization

Torque owns the **workspace**, not the occupant.

The vehicle — boot-dir materialization, process lifecycle, turn / steer /
cancel / resume, event normalization, worktree isolation, permission posture,
recovery — is core, built on `agentkit` and `go-agent-wrapper`. That is dispatch
mechanics and Torque should be good at it; it already is, and most of what it
knows was earned by incident.

What Torque does **not** own is agent identity: no portfolio catalog, no
personas, no role prose, no skills authoring. Who moves into the workspace
arrives as a `LaunchRef` resolved by a provider.

### 6.1 The ExecutionContract — always Torque's, whoever plants

```text
work identity + revision + attempt id
dispatch_brief
work_root / repo_root / base_ref  (+ the worktree decision)
loopback endpoint + capability token
effect grants
environment (non-secret) + credential references
run-context marker
```

The **loopback capability token** is a real boundary: closure-bound to
(work, attempt, session), exposing only the tools that role needs, expiring with
the binding, and authenticating the process with an unforgeable local capability
rather than trusting a caller-supplied work ID.

The **run-context marker** is `agent-setup`'s idea, adopted verbatim in spirit:
every Torque-dispatched agent is told `RUN CONTEXT: runtime-managed` — no human
is reading each turn, finish or fail through the runtime's own checkpoint and
report calls, escalate only through the channel the runtime supplied. That one
line does more for orchestrated-run behaviour than a page of rules.

### 6.2 Two paths, chosen by capability

```go
type LaunchProvider interface {
    ID() string
    Capabilities() LaunchCapabilities  // MaterializesBootDir, SupportsResume, SupportsSteer, …
    Resolve(ctx, LaunchRef) (ResolvedLaunch, error)   // → immutable digest
    Prepare(ctx, ExecutionContract, ResolvedLaunch) (PreparedLaunch, error)
}
```

**`MaterializesBootDir == false`** — Torque plants. The built-in planter is
Cairn-shaped, built on `agentkit/agentruntime/bootdir` and
`agentkit/agentcontext`:

- **templates** — one document with slot markers, so reordering it is one edit
  in one file
- **trees** — whole directories copied with their shape intact, which is what
  Torque's task bundle actually is and what it should be declared as
- **files** — single documents
- **settings** — provider-keyed, and **owned by key, not by file**: Torque writes
  what it declares and leaves every other key in a harness-owned file byte for
  byte, at every depth
- **skills / prompts / subagents** — named, resolved from a root

Default composition for `launch.projectdir`: the project's own `AGENTS.md`,
plus Torque's task-bundle tree, plus the loopback MCP config, plus the
run-context marker. That is the "users supply their own agents" story — the repo
is the agent, and Torque adds only what a dispatched run needs.

**`MaterializesBootDir == true`** — the provider owns the directory. Torque
passes the `ExecutionContract` as inputs and receives a **boot report** back
(Cairn's JSON contract shape: per-key value plus contributors). Torque records
the report's digest in the dispatch binding and does not inspect or second-guess
the contents.

### 6.3 What survives of today's launch stack

- `TORQUE_WORK_ROOT` / `TORQUE_REPO_ROOT`, the same-sibling-depth worktree rule
  and the relative-`replace` guard, the git-repo precheck, the base-ref
  fallback chain, the deterministic non-interactive permission mode. All of it
  was earned by incident and none of it should be re-derived.
- The planted task bundle, restated as a `tree` rather than as bespoke code.
- The vehicle itself. `internal/runtime/agent` is not exiled to a plugin; it is
  cleaned up, split into `internal/runtime/session` (the vehicle) and
  `internal/executor/agent` (the actor adapter), and stripped of catalog
  ownership.
- **`internal/launchprofile`, `internal/agentfile` and `profiles.yaml`'s agent
  catalog do not survive** as a user-facing catalog. That is the *only* part of
  today's agent stack that leaves. What remains is a small registered
  **internal-agents** set — planner, reviewer, orchestrator — that is Torque's
  own automation with narrow grants, named as internal, and not presented as a
  catalog anyone should author into.

## 7. Gates

Core owns the **decision record**, because who was asked and what they were
authorized to decide is coordination:

```text
gate: id  work_id  attempt_id  type  schema_revision
      reason  requested_decision
      eligible_responders  quorum_policy  deadline  on_timeout
      emitter_principal  state
      responses[]: { responder_principal, decision, payload, authenticated_at }
      applied_effect
```

Three properties adopted from Tangent, into the core contract rather than into a
surface:

1. **Idempotent open.** `(caller_scope, idempotency_key)` returns the same
   durable handle on retry.
2. **Resumable completion.** A wait answered within N seconds returns inline;
   past that it returns a **successful pending receipt carrying a durable
   handle** — never an error, never a cancellation. Transport loss, caller
   timeout and process restart stop only the waiter, not the gate.
3. **Authority is a principal, not a locator.** A gate ID is addressing. The
   right to respond comes from an authenticated principal matched against the
   eligible-responder policy. Caller-supplied names are provenance.

Torque keeps its current instinct of **rejecting a required policy it cannot
enforce deterministically** — an advisory prompt is not a required gate.

**Two things stop being one thing.** A *human decision gate* (authority, quorum,
response policy) and an *attempt resume point* (runtime continuity) share a
vague "checkpoint" abstraction today because both pause something. They become
`gates` and `attempt_checkpoints`, separately.

`GateSurface` is a plugin contract: `gate.local` (HTTP + MCP + SSE, first-party
default, no UI required) and `gate.tangent`.

## 8. Messaging

Deleted outright as Tether's job: `internal/federation` (mTLS hop auth,
authority allowlist), `internal/broker` (envelope dispatch), and
`internal/messaging`'s transport. Roughly 4,900 LOC.

What remains is two narrow adapter contracts with first-party defaults:

- **`Notifier`** — how a gate reaches a human. Default: SSE + the MCP surface.
  Composed: `notify.tether`.
- **`Steerer`** — how a message reaches a running attempt mid-turn. Default:
  `steer.loopback`, using the loopback capability the attempt already holds.
  Composed: `steer.tether`.

Two invariants worth stating in the contract:

- **Durable storage precedes best-effort live wake.** A steer that cannot be
  delivered now is not lost.
- **A prose message never causes a work transition.** Only a typed, authorized
  command or gate response moves work state. This is the same principle as
  "deterministic lifecycle must not depend on a model remembering to emit a
  magic comment prefix."

## 9. Harness and supervision

Under a coordination-only core, reflexes and steering are properties of a
running agent session — the executor's domain. Two pieces are coordination and
stay:

- **Attempt supervision** — heartbeat, last-activity gate, lease expiry,
  timeout, stuck classification, recovery decision. Today's
  `internal/runtime/stuck` policy, keyed off the executor's *normalized* events
  rather than provider specifics.
- **The conjunction principle**, as a stated constraint on every readiness
  predicate and stuck detector Torque or a plugin defines: *single detectors
  false-positive.* Nanite paid for this rule; Torque should inherit the rule,
  not the code.

Everything else — `inject_reminder`, `force_tool_choice`, drift-echo detection,
context compaction, handoff — belongs to `executor.agent.cli`, which should
adopt Nanite's engine shape (class-bound seeds, per-agent opt-out, a `required`
flag that removes opt-out from kill switches) rather than reinvent it.

## 10. Surfaces

### MCP

Core target is roughly thirty tools, from 122:

```text
torque_work_*        create get list update transition link unlink
torque_scope_*       get config_get config_set adopt
torque_policy_*      get list create revise
torque_attempt_*     list get cancel retry
torque_dispatch_*    binding_get explain
torque_ready_*       explain
torque_gate_*        list get emit respond cancel
torque_evidence_*    add list
torque_event_*       list
torque_scheduler_*   status toggle
torque_health
```

Everything else arrives as a **plugin-contributed tool family**, present only
when its plugin is loaded. Two consequences:

- Capability discovery reports the live contract set rather than a compiled-in
  list.
- The catalog refreshes through `notifications/tools/list_changed`. Today
  toggling a feature flag requires an MCP process restart — a deployment
  procedure leaking into the domain contract.

### GUI

Leaves the core binary into **its own repository**; `make build-prod` stops
embedding it. It becomes an ordinary consumer of the HTTP API.

The consequence worth naming: the HTTP API acquires an out-of-tree consumer and
therefore becomes a real versioned contract rather than an internal detail the
embedded SPA happened to call. That is a cost, and it is also the discipline
that makes the surface cut above verifiable.

(Tangent is not a substitute — it is an interaction surface, not a board. The
broader portfolio GUI story is out of scope here.)

## 11. Packaging

Following Nanite's mechanics and Hadron's boundary discipline.

```text
internal/plugin/builtin/<id>/       package + embedded plugin.yaml + init() → RegisterPlugin
internal/plugin/allplugins/         blank-imports every first-party plugin
cmd/torqued/                        blank-imports allplugins
```

**Two tiers, one contract.**

| | Hot path | Cold path |
|---|---|---|
| Contracts | executor, launch provider, readiness predicate, lifecycle overlay, gate surface | importer, notifier, evidence verifier, cost provider, audit sink, UI projection |
| First-party | compiled-in Go constructor | compiled-in or subprocess |
| Third-party | compiled-in via embedding Torque as a library | subprocess `plugin-sdk` over JSON-RPC stdio |
| Verification | build-time | Ed25519 against a signed catalog |

Both tiers declare the same manifest — contract and version, config schema,
effects and idempotency, required scopes and operations, network/filesystem/
process access, credential references, execution targets, health, retention
implications. The tier is a deployment fact, not a different model.

**From Hadron: a declared stock boundary.** A single asserted list naming the
exact contracts and capabilities the shipped `torqued` exposes. Other first-party
plugins in the repo are embeddable contracts, not capabilities the stock daemon
advertises. This is what keeps "everything is a plugin" from degrading into
"nobody can tell what a fresh install actually does."

**From Nanite: a `devmode` build tag** that disables signature verification,
which `make build` must never set.

Net install story: **`brew install torque` gives one binary that works.** The
seam is real — same contract, same manifest, same capability model — but for
first-party it is a link-time boundary, not a process boundary.

## 12. Storage

One store. SQLite is the local-first default; Postgres is a peer, not a DSN
swap. Keep `sqlstore`'s serialized-writer plus bounded-reader split exactly as
it is — `AGENTS.md` is explicit that this is solved.

**The queue moves into the primary store.** Today `internal/runtime/queue` is a
separate `queue.db`. Work transition, attempt creation, dispatch intent and work
event emission must share one commit boundary; a crash between two databases
must not leave a work item active with no runnable attempt. With an append-only
event log this becomes natural: the dispatch intent *is* an event, and a
transactional outbox drains it. `go-queue` stays for high-volume telemetry,
which is allowed to be lossy.

`go-scheduler v0.2.0` is the durable timed-activation substrate for recurring
and scheduled work (CW-20260904-0163), and is also the `wait.ActivationScheduler`
`go-workflow` expects from a host.

### Start clean, then import

The vNext schema is **greenfield**. The 31-migration chain is not extended and
not migrated; migrations restart at `001` against the record model in §4.

The live `CW-*` board crosses over through an **importer**, not a migration:

- **IDs are preserved.** `CW-*` identifiers appear in Tesseract records, git
  commit messages, prose across five repositories and Chrispian's own working
  memory. An import that renumbers is a failed import.
- Work items, edges, comments, gates (from `checkpoints`), evidence (from
  `artifacts`), and scope membership map across directly.
- The work-event log is seeded with one synthesized `imported` event per item
  carrying the v1 row verbatim, plus real transition events wherever they are
  derivable from `run_events`. History before the import is honest about being
  reconstructed rather than pretending to be native.
- `run_events` telemetry is **not** imported. It is bounded-retention
  observation and there is no reason to carry it.
- The importer is re-runnable against a v1 snapshot, and its output is
  diffable, so the cutover can be rehearsed rather than performed once.

## 13. Honest accounting

Rough shape of the change, so nobody discovers it later:

| Package | LOC | Disposition |
|---|---|---|
| `internal/runtime/agent` | 12,200 | **stays core**, split into `runtime/session` (vehicle) + `executor/agent` (actor adapter), stripped of catalog ownership |
| `internal/runtime/scheduler` | 12,500 | stays core; readiness and claims rework |
| `internal/mcpadapter` | 16,500 | ~⅓ stays core, the rest moves with its plugin |
| `internal/persistence` | 12,300 | schema replaced greenfield; the writer/reader split and `go-sqlite` pattern preserved verbatim |
| `internal/messaging` + `federation` + `broker` | 4,900 | deleted; ~200 lines of adapter contract remain |
| `internal/service` | 11,600 | rewritten onto the new records; domain logic largely survives |
| `internal/planner` + `planstart` + `orchestrator` | 1,870 | deleted, rebuilt as `executor.workflow` on `go-workflow` |
| `internal/launchprofile` + `agentfile` | 950 | deleted; becomes `LaunchRef` + provider registry |
| `internal/hitl` | 660 | policy survives into `gates`; surface becomes a plugin |
| `apps/gui` | — | separate repository |

On the order of **25,000–30,000 LOC of 113,000 moving or dying**, with a further
~25,000 rewritten in place against new records. The mechanics survive — the
SQLite concurrency pattern, the scheduler loop, the session vehicle, the
worktree rules, the MCP plumbing. What changes is the domain model, the
boundary and the surface.

## 14. Open questions

For a following session to work down. Numbered so they can be cited.

1. **`dispatch_brief` vs. `execution_policy`.** Does putting authored prose on
   the work item and grants on the policy correctly split "data" from
   "capability"? (§4.2 asserts yes.)
2. **Scope config authority.** File-authored with explicit adopt, as §5
   proposes, or database-only with the file as export?
3. **Does the canonical FSM need `proposed`?** Or is `open` the entry state and
   intake a `kind`?
4. **Are `epic` / `sprint` / `collection` one plugin or three?**
5. **Daemon or library?** Is `torqued` always the authoritative runtime with CLI
   and MCP as clients, or is a fenced embedded mode also a product contract?
   (Direction doc Q8, still open.)
6. **The smallest useful plugin manifest.** What must a Torque `plugin.yaml`
   declare, concretely, for the capability model in §11 to be enforceable?
7. **Is the work-event log externally subscribable** — a real event bus — or is
   SSE plus a cursor endpoint enough for v1?
8. **Sequencing.** Which of this is gated on Tether's launch hosting and
    `go-app-agent` landing (CW-20260906-0091, CW-20260904-0103), and which can
    proceed now? The core/plugin split and the record decomposition appear to be
    entirely local work.
