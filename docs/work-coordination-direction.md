# Torque Work Coordination and Execution Direction

**Status:** Directional architecture draft

**Date:** 2026-08-22
**Scope:** Desired product boundaries and architecture; not an implementation plan

## Purpose

This document describes Torque's intended place in the Hollis Labs portfolio.
It evaluates work items, plans, projects, sprints, scheduling, agent execution,
human gates, messaging, workspaces, artifacts, credentials, persistence,
observability, and extension points against the portfolio's shared engineering
boundaries.

It deliberately does not define phases, estimates, task breakdowns, migration
steps, or compatibility strategy. Existing ADRs and current documentation
remain historical and implementation truth until explicitly superseded. This
document provides a target direction for a later architecture and planning
session.

## Portfolio axiom

> Hollis tools own execution and operational state, but not the business
> definitions or business data they operate on.

For Torque, managed-work records are part of its operational domain. The
axiom therefore means:

- Users and projects own why work exists, what its product language means,
  the underlying specification, and the authority to accept the outcome.
- Source repositories and business applications own their files, entities,
  and produced deliverables.
- Publishers own reusable task templates, launch profiles, agent definitions,
  review policies, and other authored execution definitions.
- Executors and called systems own their provider-native execution state.
- Credential authorities own secret material and its lifecycle.
- Torque owns the canonical coordination record for work deliberately managed
  through Torque: work identity, lifecycle, dependencies, priority,
  assignment, readiness, scheduling policy, review state, gates, dispatch
  bindings, run correlation, evidence references, cost observations, and
  audit history.

Torque being authoritative for a task does not make it authoritative for the
repository change, product requirement, external issue, deployment, message,
or workflow that task refers to. It owns the work-management representation
and the operational facts produced while coordinating it.

## Product definition

Torque is a local-first durable work coordination and dispatch control plane
for human and agent work. It:

1. Maintains a canonical graph of managed work items and their relationships.
2. Separates work lifecycle from dispatch readiness, execution attempts,
   agent sessions, and acceptance.
3. Evaluates dependency, policy, budget, gate, concurrency, and capability
   conditions to decide what work is eligible to run.
4. Binds an eligible work item to an immutable execution request.
5. Dispatches that request through a registered executor and controls retry,
   cancellation, timeout, and recovery semantics.
6. Correlates runs, sessions, checkpoints, comments, evidence, costs, and
   after-action feedback with the work item.
7. Supports projects, plans, phases, epics, sprints, and collections as
   coordination structures rather than as a general project-management suite.
8. Exposes the same work model through CLI, HTTP, MCP, GUI, events, and narrow
   integration contracts.

Torque is not a general workflow engine, universal agent platform, conversation
product, generic message broker, source repository, artifact store, knowledge
base, secret manager, infrastructure control plane, or Coder workspace
provider.

Calling Torque a task orchestration runtime is accurate when orchestration
means coordinating work items and dispatches. It should not imply that Torque
owns arbitrary business workflows; that is Hadron's domain.

## Architecture sketch

```text
Users / projects / source systems
authoritative intent, specifications, acceptance, and deliverables
                         |
                         | explicit creation, import, or source reference
                         v
               +-------------------------+
               |         Torque          |
               |                         |
               | work graph              |
               | lifecycle + readiness   |
               | scheduler + leases      |
               | gates + review          |
               | dispatch bindings       |
               | events + audit          |
               +------------+------------+
                            |
                   immutable Dispatch
                            |
        +-------------------+--------------------+
        |                   |                    |
        v                   v                    v
  direct agent         Hadron workflow      external/manual
  executor             executor adapter     executor adapter
        |                   |                    |
        +-------------------+--------------------+
                            |
                    result + evidence refs
                            |
                    Torque work decision

Cerberus ---- WorkspaceHandle + WorkspaceLease ----> execution target
Tether ------ optional MCP/LLM/message/session ------> adapters
Tesseract --- explicit memory/knowledge capture ----> durable learning
```

The scheduler chooses and binds work. The selected executor owns its native
operation. Torque interprets the normalized result only according to the work
item's declared coordination policy.

## Responsibility boundaries

| Concern | Authoritative owner | Torque responsibility |
|---|---|---|
| Product requirement or external issue | Project or source system | Store a source reference or explicitly managed work representation |
| Torque-native task | User/project through Torque | Preserve identity, state, relationships, policy, and history |
| Imported task | External source unless explicitly adopted | Cache/project with provenance and synchronize under declared policy |
| Plan and phase coordination | User/project through Torque | Maintain work graph, progress, and coordination state |
| Workflow definition and node semantics | Workflow publisher / Hadron | Hold an immutable workflow reference and correlate its run |
| Agent definition and launch profile | Publisher or calling scope | Resolve, validate, snapshot, and bind for a run |
| Task-scoped agent run | Torque | Own dispatch, task correlation, lifecycle policy, and completion handling |
| Nanite or Tether session | Nanite or Tether | Store explicit session/run binding and observe through the adapter |
| Workspace resource and lease | Cerberus and Coder | Request, bind, renew, and release for a Torque run |
| Git repository and work product | Project/source authority | Prepare a work root and retain evidence references |
| General messages and federation | Tether when composed | Own task meaning; use a local scoped adapter when standalone |
| Comments and review discussion | Torque work domain | Preserve task-scoped discussion and actor provenance |
| Artifact content | Project or artifact authority | Store evidence metadata, digests, and locators; inline only bounded native records |
| Memory and knowledge | Tesseract namespace authority | Explicitly write or promote approved outcomes and learnings |
| Provider secrets | External credential authority | Carry references and resolve scoped grants at the execution boundary |
| Torque access credentials | Torque or external identity authority | Enforce principal identity and service-local authorization |

## Core domain model

### Work scope and project reference

A Torque deployment manages one or more explicit work scopes. A scope defines
the ownership, policy, and visibility boundary within which work is
coordinated. It is not a Coder workspace and should not be named one.

A project record is primarily a reference to an externally owned project:

```text
ProjectRef
  project identity
  owning authority
  display metadata
  source/repository references
  default policy references
  default launch and execution-target references
  provenance
```

Repository paths, agent paths, read/write paths, rules, and permissions may be
materialized local observations or configuration. They do not make Torque the
owner of the repository or project definition.

### Work item

A work item is the smallest independently coordinated unit:

```text
WorkItem
  identity and scope
  kind
  title and bounded work brief
  source authority and reference
  lifecycle state
  readiness conditions
  priority and ordering
  assignment and dispatch policy
  dependency relationships
  acceptance and evidence requirements
  policy and execution references
  provenance and revision
```

A native work item may be authored directly in Torque and be canonical there.
An imported work item remains a projection unless an explicit adoption action
transfers its work-management authority to Torque. `source_type` and
`source_ref` are provenance; they are not by themselves an authority-transfer
protocol.

### Work-item kinds

Kinds express coordination semantics, not merely UI labels:

- A task is actionable work that may be assigned or dispatched.
- An issue is intake or a problem statement that is not automatically
  executable.
- A decision requires an authorized decision or approval.
- An external item is performed outside Torque and completed by evidence or a
  source observation.
- A coordination container groups child work but is not itself an executor
  job.
- An external condition blocks readiness until a declared observation is true.
- A system job performs bounded Torque maintenance and is always visible in
  operational audit even when hidden from normal work views.

The current `task.kind` approach is a useful shared identity model. The target
should avoid forcing plans, issues, waits, internal automation, and executable
tasks through one undifferentiated row, lifecycle, and validation path when
their semantics differ. They may share a `WorkNode` identity and graph while
using kind-specific contracts.

### Work graph

Dependencies, parent-child relationships, plan membership, phase membership,
epic membership, and sprint commitment form an explicit graph. Relationships
are typed and independently addressable:

```text
WorkEdge
  from work identity
  to work identity
  relation kind
  ordering or condition
  provenance
  created by actor
  active lifecycle
```

A missing or deleted dependency must not silently make a task runnable or
permanently wedge it. Deletion, retirement, and relationship removal have
declared semantics and durable history.

Cycles are rejected for relation kinds that require a directed acyclic graph.
Not every grouping relation needs to impose execution order.

### Plan and phase

A Torque plan is a mutable coordination structure over work items. It answers:

- what work is in scope
- how it is grouped
- which dependencies and gates constrain it
- how progress and acceptance roll up
- who may refine or approve the plan

A plan is not a Hadron workflow definition. It does not define arbitrary data
flow, processors, branching execution language, or provider-specific step
semantics.

Phases should be first-class plan entities or typed graph nodes rather than
opaque arrays inside a task metadata blob. A phase has stable identity,
ordering, acceptance policy, and history. Removing or reordering a phase is a
domain event that external observers can consume reliably.

Agent-produced planning output is a proposal. It can suggest ordering,
acceptance changes, new work, or redundancy, but it does not become the
authoritative plan merely because an internal planner wrote JSON into plan
metadata.

### Epic, sprint, and collection

These concepts remain useful within a deliberately bounded work-management
product:

- An epic groups work toward a durable outcome.
- A sprint is a timeboxed commitment, budget, and review boundary.
- A collection is a user-controlled triage or presentation grouping.

They do not turn Torque into a general portfolio-planning suite. Rich product
roadmaps, organization charts, customer records, financial planning, and
arbitrary business objects remain outside Torque.

Archive is visibility and retention policy, not completion. An epic can be
complete but unarchived; an abandoned sprint can be archived; a collection can
be archived without altering the tasks it contains.

### Templates

Reusable work templates are authored definitions. A publisher owns their
content and organization. Torque may ship built-ins and may validate, cache,
index, instantiate, and materialize external templates.

Instantiation records:

- immutable template identity, revision, and digest
- supplied variables and non-secret effective configuration
- provenance and caller
- resulting work identities

The instantiated work is Torque-managed state. The template source remains
publisher-owned.

## Lifecycle separation

Torque needs several explicit state machines instead of one status field that
attempts to describe everything.

### Work lifecycle

A directional work lifecycle is:

```text
Proposed -> Open -> Active -> InReview -> Completed
              |       |          |
              +-------+----------+-> Blocked
              |       |          |
              +-------+----------+-> Abandoned

Completed / Abandoned -> Archived
```

The exact names may remain compatible with `todo`, `doing`, `review`, `done`,
and related current values. Their meanings should be explicit:

- Open means committed work exists; it does not mean dispatchable now.
- Active means work has been claimed or is being coordinated; it does not
  prove a process is currently running.
- InReview means execution evidence awaits acceptance.
- Completed means the authorized acceptance policy was satisfied.
- Blocked identifies a current impediment and its resolution conditions.
- Archived controls normal visibility and retention; it is not a substitute
  for a terminal outcome.

### Readiness

Readiness is a derived condition over a work item and current system state:

```text
Ready =
  work is open
  + dependencies satisfied
  + required gates satisfied
  + dispatch policy permits activation
  + budget available
  + executor capability available
  + execution target ready
  + no active exclusive claim
  + authorization permits dispatch
```

`Open` and `Ready` are not synonyms. The current `todo` bucket includes both
eligible and ineligible work; the target exposes readiness and its reasons
directly rather than making operators infer them from scheduler skip logs.

### Dispatch policy

The current `manual` boolean combines several intentions. The target uses an
explicit policy such as:

```text
Manual
Automatic
ExternallyActivated
NeverDispatch
```

Human work, external work, plan containers, decision gates, and paused
automatic work no longer depend on a single inverted boolean to avoid
accidental execution.

### Attempt and run lifecycle

An execution attempt is distinct from the work item:

```text
Planned -> Queued -> Leased -> Starting -> Running -> Waiting
                                      |         |         |
                                      v         v         v
                                   Failed   Succeeded   TimedOut
                                      |         |
                                      +----> Canceled / Lost / Superseded
```

A run succeeding means the executor fulfilled its invocation contract. It is
evidence for work completion, not automatic acceptance. A run can succeed and
send the task to review. A task can be completed manually without an executor
run. A canceled or superseded run must not overwrite a later operator decision.

### Session lifecycle

An agent session is another independent identity. A single run may create one
or more sessions; a reusable session may participate in multiple related runs
when policy permits. `running session`, `running attempt`, and `active task`
must never be treated as interchangeable facts.

### Gate lifecycle

Human approval and external wait conditions have their own proposal,
notification, response, expiry, cancellation, and application states. Parking
a task in review is a work consequence of a gate; it is not the gate record
itself.

## Scheduling and dispatch

### Scheduler responsibility

The scheduler:

- evaluates readiness and eligibility
- applies priority, fairness, and concurrency policy
- acquires an exclusive or declared shared claim
- creates an immutable dispatch binding
- queues and starts an attempt
- renews leases and observes heartbeats
- cancels, times out, or recovers lost attempts
- applies the normalized result through work policy

The scheduler does not invent task intent, approve its own work, edit project
files, or interpret natural-language output as an authorized state transition.

### Immutable dispatch binding

Every attempt should be explainable by a release-like binding:

```text
DispatchBinding =
  work item identity + revision
  + dependency/readiness snapshot
  + dispatch and acceptance policy
  + executor registration + version
  + resolved launch definition revision
  + normalized input
  + tool and effect grants
  + execution target
  + optional WorkspaceHandle + WorkspaceLease
  + source checkout / work-root binding
  + non-secret effective configuration
  + credential references
  + caller, actor, causation, and correlation
```

Secret values are resolved only at the narrow execution boundary and are not
part of the persisted dispatch document.

Changing a task while an attempt is running does not silently change that
attempt. A later retry or redispatch receives a new binding from the current
work revision and policy.

### Claims, leases, and idempotency

Execution is at-least-once unless an executor provides a stronger contract.
Torque should not promise exactly-once effects merely because a task has one
current status.

A dispatch claim records owner, attempt, lease expiry, renewal, and fencing
token. Recovery may requeue only after proving the prior claim expired or was
revoked. Executors receive an idempotency key and effect policy where their
target supports it.

Task transition, attempt creation, dispatch intent, and durable event emission
share one commit boundary. If a separate queue is used, an outbox or equivalent
reconciliation protocol bridges the stores; a crash between databases must not
leave a task indefinitely active with no runnable attempt.

### Retry and escalation

Retry policy is bound per attempt series and classifies failures as transient,
permanent, policy, cancellation, lost worker, or incomplete evidence. A retry
creates a new attempt; it does not erase the prior result.

Escalation may alter priority, request a different executor, ask for human
guidance, or block the item. It must not silently broaden permissions, switch
to a more powerful agent, or bypass acceptance authority because a retry count
was exceeded.

## Executor model

An executor is a registered adapter from an immutable dispatch binding to a
normalized result stream. It declares:

- supported work and input kinds
- lifecycle and cancellation behavior
- streaming, tool, resume, and checkpoint capabilities
- execution-target requirements
- effect and idempotency properties
- credential references it may consume
- result and evidence schemas
- health and readiness behavior

Useful executor classes include:

- task-scoped agent executor
- Hadron workflow executor
- bounded command or process executor
- external-system job executor
- human/manual completion adapter

Plans, waits, and issues are not executors. A wait can influence readiness; a
plan can roll up child state; an issue can be promoted into actionable work.

Executor-specific state stays behind the adapter. Torque stores the normalized
handle, correlation, observations, and evidence needed to govern its work
contract.

## Agent execution boundary

Torque legitimately launches agents because task-bound execution is a core
executor use case. For an agent attempt, Torque owns:

- the task-to-run binding
- execution policy and granted capabilities
- task-scoped boot materialization
- run/session correlation
- retry, cancellation, timeout, and completion handling
- the task-scoped callback or loopback surface
- execution evidence and operational observations

Torque does not own:

- a portfolio-wide agent catalog
- interactive conversation or team semantics
- caller-owned role, skill, or persona definitions
- Nanite or Tether sessions it did not launch
- general agent memory

Nanite, Tether, Torque, and Hadron may each launch agents for different product
reasons. The overlap is intentional:

- Nanite owns interactive agent construction, sessions, and teams.
- Tether owns sessions explicitly launched through its session substrate.
- Torque owns agent execution bound to managed-work lifecycle.
- Hadron owns an agent invocation as a node in a declared workflow.

The direct Torque agent executor should consume shared `go-agent-wrapper`
launch, boot, runtime, and provider contracts when those contracts are stable.
Torque remains the owner of task-specific semantics and does not need a Nanite
or Tether service merely to use the shared mechanics.

### Internal planning and review agents

Planner, orchestrator, reviewer, and supervisor agents can be valuable Torque
automation. They should operate as explicitly registered launch definitions
with narrow grants and clear authority.

They may propose plan changes, review evidence, summarize progress, or request
decisions. Deterministic work lifecycle must not depend on a model remembering
to emit a magic comment prefix, completion marker, or specially formatted
metadata field. The harness records known operational facts; agents contribute
judgment as proposals or evidence.

Hidden `internal` work must remain inspectable and auditable. Hiding system
jobs from a default human task list is a presentation choice, not permission to
create invisible state-changing work.

## Workspaces and execution roots

Coder is the portfolio's workspace provider beneath Cerberus. Torque consumes
it as one possible execution target and does not provision or manage Coder
directly.

```text
Torque dispatch
      |
      v
Cerberus workspace acquire
      |
      v
WorkspaceHandle + WorkspaceLease
      |
      v
Torque binds executor and attempt to the ready target
```

Task, attempt, agent session, workspace, and workspace lease remain separate
identities. `Provisioned` is not `Ready`, and Torque may dispatch only when the
conditions required by the bound executor are ready.

The target supports both execution placements defined by Cerberus:

```text
remote_tools
  Agent loop remains outside; filesystem and process operations are mediated
  into the workspace.

workspace_local
  The agent process and its native tools execute inside the workspace.
```

Local execution remains valid without Cerberus. A local directory or Git
worktree is an execution root, not a Workspace.

### Root vocabulary

Torque's current four-root model is useful but should avoid overloading the
portfolio's Workspace term:

```text
project_root   canonical source checkout reference
work_root      writable execution checkout or mount
run_state_root durable Torque logs and session metadata
build_root     ephemeral provider boot materialization
workspace      only a Cerberus/Coder WorkspaceHandle when one is used
```

The go-apppaths `TORQUE_WORKSPACE` selector is a data-instance/profile concept,
not a compute workspace. Compatibility may preserve the variable, but the
public architecture should use an unambiguous term.

### Source checkout and Git worktrees

A per-run Git worktree is a `SourceCheckout` or `WorktreeLease`, not a compute
workspace. Torque may manage it as part of local execution because it is
temporary run state over a project-owned repository.

The binding records the requested source ref, resolved immutable commit,
checkout path, ownership, cleanup policy, and dirty-state disposition. Once an
agent modifies it, the project owns the resulting work product.

If isolation is required by policy, a failure to create the isolated work root
must fail closed. Silently falling back to the shared project checkout changes
the safety contract and is not an acceptable convenience.

## Launch definitions and configuration

A launch profile is an authored definition reference, not a loose alias that
becomes whatever a mutable file says at dispatch time.

```text
LaunchProfileRef
  publisher authority
  logical identity
  source locator
  requested version or constraint
  resolved immutable revision and digest
```

Resolution produces a compiled launch definition. Dispatch combines that
definition with task-owned dynamic inputs to form the immutable binding.

Torque may ship built-in launch families and allow operator registrations.
Caller-supplied definitions remain inputs unless explicitly registered by an
authorized publisher. Compatibility aliases resolve to a canonical identity
and their provenance remains visible.

Configuration precedence should be one explainable model across:

```text
operator policy
  -> work-scope / project policy
  -> launch definition
  -> task request
  -> attempt override
```

Later layers may narrow or specialize policy but cannot silently elevate
permissions, budgets, network access, or credential scope beyond their
authority.

Task content and execution control are separate. An imported or untrusted task
description is data shown to an executor; it is not a system prompt, tool
grant, environment grant, or permission policy. Fields such as system prompts,
tools, permissions, environment, and launch profiles require stronger
publisher authority than ordinary work-description edits.

## Tools, effects, and permission policy

Torque needs one effective-policy compiler over several enforcement points:

- who may change work state
- what tools the task requests
- what tools the executor can provide
- what the launch definition permits
- what the project and operator allow
- what the execution target permits
- which calls need human approval
- what the selected MCP gateway grants

The result is an immutable `EffectGrant` set attached to the dispatch binding.
It uses stable tool identities and effect classes rather than relying only on
tool-name string matching.

Task allowlists, Torque-hosted MCP permission modes, provider-native permission
modes, sandbox policy, and workspace policy remain distinct enforcement layers
but derive from the same effective decision. A profile cannot grant itself
`bypassPermissions` when the project or operator forbids it.

The task-scoped MCP loopback is a strong capability boundary. Its identity
should be closure-bound to task, attempt, and session; expose only the tools
needed for that role; expire with the binding; and authenticate the process
using an unforgeable local capability rather than trusting a caller-supplied
task ID.

Tether's MCP gateway is optional composition for discovery, routing, and
gateway policy. Torque keeps the task-effect decision even when Tether carries
the tool call.

## Human decisions and checkpoints

Torque owns human gates that govern managed work. A decision request records:

- work item and attempt correlation
- typed workflow and schema revision
- reason and requested decision
- eligible responders and quorum policy
- emitter identity
- notification references
- deadline and timeout behavior
- authenticated responses
- application of the decision to work state

Torque must reject a required policy it cannot enforce deterministically. An
advisory prompt is not a required gate.

HITL decision gates and agent session resume points are different concepts.
The former carries human authority and response policy; the latter carries
runtime continuity. They should not share a vague checkpoint abstraction just
because both pause something.

A response author is derived from authentication. Caller-supplied display
names and source references are additional provenance, not authority.

## Comments, messages, artifacts, and AARs

### Comments and task discussion

Comments are native work records. Torque may own their custody because they
explain decisions, state changes, reviews, and handoffs within a work item.
They require immutable identity, authenticated actor, edit history, and
retention policy.

A comment is not automatically a general mailbox message. Task discussion can
be rendered or delivered through messaging without making the two storage
models identical.

### Messaging

Torque needs bounded communication for task notifications, steering,
checkpoint delivery, and execution correlation. In standalone mode it may use
a local `go-messaging` store and adapter for these scoped needs.

Tether owns the portfolio's general messaging and federation capability when
composed: durable cross-application delivery, subscriptions, public addressing,
and federation routing. A Torque deployment using Tether should not create a
second owner for the same address authority or inbox.

Torque remains authoritative for what an envelope means to a task. Tether does
not infer a task transition from prose; Torque applies a typed, authorized
command or event through its own service boundary.

Generic mTLS federation, authority routing, and shared envelope mechanics
belong in Tether or shared libraries rather than growing as a second
portfolio-wide messaging product inside Torque. The local adapter preserves
Torque's standalone operation.

### Events are not messages

A work-domain event is a durable fact that something happened. A message is a
delivery object between participants. An event may cause a message, and a
message may request a command, but neither substitutes for the other.

### Evidence and artifacts

Torque stores evidence that an acceptance requirement was addressed:

```text
EvidenceRef
  identity and kind
  task and attempt
  authoritative locator
  immutable digest or revision
  producer and observation time
  verification assertion
  bounded metadata
```

Repository diffs, commits, pull requests, screenshots, test reports, build
outputs, and deployment records normally remain in their authoritative
systems. Torque may inline bounded, Torque-native evidence such as a short
note or structured decision response. It should not become a general binary or
document store.

An executor claiming that a deliverable exists is not the same as verifying
the referenced artifact. Acceptance policy decides the required evidence and
who may verify it.

### After-action reports

AARs are valuable execution feedback. Torque should stamp every operational
fact it already knows—task, attempt, executor, model, timing, cost, error class,
and outcome—without requiring an agent to repeat them correctly.

Agent reflection remains optional authored content attached to the run. A
missing or vacuous reflection is a signal, not a reason to fabricate one. The
agent's self-classified outcome is distinct from executor result and task
acceptance.

Approved durable learnings may be written or promoted into Tesseract through
an explicit policy. Raw AAR content does not automatically become canonical
memory.

## Events, audit, and observability

### Durable work events

Every material change to managed work should emit a transactional domain
event:

```text
WorkEvent
  event identity and schema version
  work scope and entity identity
  event kind
  authenticated actor
  causation and correlation
  command identity / idempotency key
  prior and resulting revision
  policy decision
  occurred and recorded times
  bounded payload
```

The current entity rows are projections of those facts. Event history makes
forced transitions, priority changes, dependency edits, plan refinements,
assignments, archive operations, and policy overrides explainable rather than
leaving only the latest row.

Not every streaming token or heartbeat is a work-domain event. High-volume run
telemetry can use a separate retention and storage class.

### Live events

An in-process bus and SSE stream are delivery projections. They may be
best-effort and bounded because consumers can resume from a durable cursor.
A dropped live event must not be the only record of a state change or the only
trigger for required lifecycle behavior.

Internal lifecycle correctness should use committed domain state and durable
events, not prompt-authored marker comments or a drop-on-full in-memory bus.

### Audit

Audit covers:

- commands accepted and rejected
- actor and effective authority
- work revisions and transitions
- readiness and dispatch decisions
- policy compilation and capability grants
- attempts, leases, retries, cancellation, and recovery
- tool calls and approvals
- workspace and source-checkout bindings
- model/provider calls and cost sources
- checkpoint emission, response, and application
- artifact/evidence verification
- plugin and adapter activity
- administrative repair, import, export, backup, and deletion

Logs go to stdout and stderr. Traces and metrics explain runtime behavior.
Canonical audit facts are durable structured data and are never recovered by
scraping logs.

## Persistence, queueing, and recovery

### Authoritative state

Torque's authoritative store contains:

- work scopes, project references, work items, graph edges, and revisions
- plan, phase, sprint, epic, collection, and gate state
- dispatch intents, claims, attempts, and normalized results
- comments, bounded native evidence, and AAR references
- durable domain events and canonical audit facts
- policies, registrations, and service-local authorization state

### Operational and rebuildable state

- live subscriptions and SSE buffers
- scheduler tick observations
- transient worker registries
- cached readiness projections
- derived progress and cost rollups
- temporary build roots
- recreatable search and UI projections

Queue state is operational but not disposable while it represents committed
dispatch intent. It either lives in the authoritative transactional store or
has a durable outbox/reconciliation protocol.

### SQLite and Postgres

SQLite is appropriate for the local-first, single-authoritative-daemon shape.
It should have one writer authority, bounded readers, explicit migrations, and
crash-safe reconciliation.

Postgres is appropriate when a deployment needs multiple service processes,
remote clients, stronger concurrent scheduling, or database-native leasing.
Backend support is a semantic promise, not merely a DSN switch: transitions,
locking, migrations, ordering, time handling, and queue claims need contract
tests against every advertised backend.

The application should not continuously compensate for an undefined middle
ground where several processes directly open one SQLite database and each
believes it owns sessions, migrations, hooks, and scheduler-adjacent state.

### Backup and restore

Backup is a complete Torque-instance contract. It identifies:

- authoritative database state
- durable dispatch/outbox state in any secondary store
- policy and registration sources required to interpret records
- externally stored evidence references
- omitted rebuildable projections
- binary/schema compatibility
- checksums and creation provenance

A main SQLite snapshot alone is complete only when every other durable store is
explicitly classified as rebuildable or captured separately. Postgres and
remote stores need equivalent semantics, not a SQLite-only side script.

Backup is a one-off admin operation from the same release. Scheduling and
supervision belong to the operating environment or Cerberus; Torque should not
require a forever-loop shell script to act as its own scheduler.

Restore verifies integrity, runs in an exclusive or fenced administrative
mode, reconciles pending dispatches, and never silently starts recovered work
before the operator-selected recovery policy is applied.

### Deletion and retention

Hard deletion of a work item can erase runs, comments, evidence, and
relationships through cascades. It is therefore a privileged, exceptional
operation with preview, actor, reason, and audit. Abandonment and archive are
the normal work dispositions.

Retention policies distinguish work history, message content, run telemetry,
agent output, AAR reflection, artifact content, and audit. Redaction or legal
deletion must not leave projections that still expose removed content.

## Daemon and process model

`torque serve` is the authoritative runtime owner for a local instance. It
owns:

- migrations and store handles
- scheduler and dispatch claims
- session/process registry
- background workers
- live event delivery
- HTTP API and GUI
- configured adapters

The operating system or Cerberus supervises this process. Torque does not need
to install, restart, or keep alive its own daemon.

CLI and stdio MCP are clients of the authoritative daemon during normal
operation. They should not open the same database, run migrations, start an
independent agent manager, and launch processes whose live handles disappear
when the client exits. A stdio MCP proxy may preserve harness compatibility
while forwarding semantic operations to the daemon.

One-off admin commands may access storage directly only under an explicit
maintenance contract that fences the daemon or uses backend-safe operations.

Health means the process is alive. Readiness means the configured store,
migrations, scheduler leadership, executor registrations, policy, and required
attached resources can serve the advertised capabilities. A healthy daemon can
be unready for dispatch.

## Authentication, authorization, and secrets

### Identity and authorization

Every mutating command has an authenticated principal and client identity.
Display author strings, source types, agent IDs, and message addresses are
claims attached to that identity; none is accepted as proof by itself.

Authorization distinguishes:

- reading work content
- creating and editing work briefs
- changing priority, assignment, and graph relationships
- editing execution-control fields
- dispatching, canceling, retrying, and forcing transitions
- responding to required decisions
- approving completion
- administering policies, executors, and plugins
- accessing sensitive run output

Loopback-only open mode can be a deployment profile, but binding a service to a
network interface requires an explicit authentication and authorization model.
Federation mTLS does not authenticate the ordinary HTTP, GUI, or MCP surface.

Forced transitions are break-glass commands. They require elevated authority,
a reason, and a durable audit fact; `force=true` is not merely another Boolean
accepted from any caller.

### Secret boundary

Torque stores secret references, never provider secret values, in task,
project, template, launch-profile, environment, plugin, or federation
configuration.

```text
CredentialRef
  authority
  logical identity
  permitted purpose
  principal / task / attempt scope
  expiry or rotation metadata
```

At dispatch, an authorized resolver converts a reference into the narrowest
runtime grant. Secret values do not enter task rows, persisted launch plans,
boot bundles, command arguments, logs, events, AARs, or backups.

Provider SDKs may be used directly behind executor adapters, or Torque may use
Tether's LLM gateway. In both cases the credential authority remains external.

An API-key helper may provide narrow resolution plumbing, but credential
discovery, OAuth refresh, keychain mutation, and general secret lifecycle are
not Torque's task-management responsibility. Those belong to a provider tool,
credential authority, or shared execution substrate.

## Plugin and adapter model

The Hollis Labs plugin SDK provides shared loading, lifecycle, configuration,
capability, diagnostics, and compatibility mechanics. Torque defines narrow
work-domain contracts above it.

Useful extension classes include:

- executor
- task or issue source/importer
- launch-definition provider
- readiness predicate
- review or acceptance policy
- artifact/evidence verifier
- execution-target adapter
- Workspace client adapter to Cerberus
- notification or messaging adapter
- cost and model metadata provider
- audit or event sink
- UI projection

Each plugin declares:

- contract and version
- configuration schema
- effects and idempotency
- required work scopes and operations
- network, filesystem, and process access
- credential references it may consume
- supported execution targets
- health and readiness behavior
- data-retention implications

Plugins receive capability-scoped services rather than an unrestricted SQL
store or arbitrary host registry. Registration is transactional, unloading
revokes registrations safely, and durable records retain the plugin identity
and version needed to interpret them.

Torque should have one executor contract. The current runtime executor and
plugin-facing executor types have drifted in fields and method signatures;
adapters should implement the domain contract rather than maintain parallel
copies.

A plugin manifest only configures code that can actually be loaded by the
declared execution model. Compiled constructors, external processes, and future
dynamic modules must be named honestly.

## Interfaces and capability discovery

Torque follows one core, several doors:

```text
Domain services
  -> HTTP
  -> CLI client
  -> MCP adapter
  -> GUI
  -> event subscriptions
  -> task-scoped loopback
```

All surfaces preserve the same identity, authorization, lifecycle, revision,
and idempotency semantics. Transport-specific interaction patterns are allowed;
transport-specific business rules are not.

Feature modules may be enabled independently, but enabled state should not
make existing data semantically disappear. Capability discovery reports the
live module set. MCP tool catalogs should refresh through protocol-supported
change notification or stable generic capability surfaces rather than relying
on a process restart as part of the domain contract.

The GUI and MCP descriptions are projections of registered contracts. Go code
and tests remain authoritative for behavior, but clients should not need to
read implementation files to discover fundamental schemas and effects.

## Portfolio composition

### Hadron

Torque and Hadron have the most important boundary:

```text
Torque: What work exists, what is ready, what should run next, and was it
        accepted?

Hadron: Given this immutable workflow definition and invocation binding, how
        is the declared workflow executed?
```

A Torque work item can select a Hadron executor and bind a pinned
`DefinitionRef`. Torque owns the work item, priority, readiness, dispatch,
budget policy, and acceptance. Hadron owns the workflow run, nodes, waits,
retries internal to the workflow, and workflow provenance. Torque stores the
Hadron run handle and normalized result.

A Hadron workflow may create or update Torque work through an authorized
adapter. The composition must avoid circular ownership where Torque waits for
Hadron to complete a task while Hadron waits for Torque to complete the same
semantic unit.

### Nanite

Nanite owns interactive agents, sessions, teams, roles, skills, and
conversation experience. It may create, inspect, or execute Torque tasks and
may serve as an agent executor or session substrate through an explicit
adapter.

Torque remains the source of truth for the assigned work and its lifecycle.
Nanite remains the source of truth for its session. A task ID in a Nanite boot
prompt is a reference, not a copy of the task body or a transfer of authority.

### Tether

Tether is optional composition for general messaging and federation, LLM and
MCP gateways, public identity, and sessions explicitly delegated to it.

Torque owns task semantics and task-bound dispatch policy. Tether owns the
operational gateway, delivery, or delegated-session state. Torque may remain
standalone with local adapters, but one deployment must not assign two systems
authority over the same inbox or agent session.

### Cerberus

Cerberus deploys and supervises Torque and manages its attached infrastructure
resources. For isolated execution Torque requests a Coder Workspace and
WorkspaceLease from Cerberus, waits for the required readiness conditions,
binds them to an attempt, and releases the lease under declared disposition.

Cerberus never interprets task completion. Torque never becomes a Coder or
infrastructure control plane.

### Tesseract

Tesseract may index pointer-backed Torque knowledge or receive explicit
decisions, outcomes, limitations, and learnings. Torque remains authoritative
for current work state. Tesseract snapshots identify their Torque source and
observation time and are never presented as current without resolving Torque.

### Other applications

Other applications create or consume work through explicit commands, source
references, events, and handles. Sharing Torque's database is not an
integration contract. A caller can use task storage without using agent
execution, messaging, Coder, or Hadron.

## Twelve-factor and Go operating model

Torque follows the portfolio's adapted twelve-factor direction:

- One version-controlled codebase produces versioned binaries and packages for
  many deployments.
- Go dependencies and required external tools are explicit and isolated.
- Configuration is external; repositories contain no environment-specific
  values or secrets.
- Databases, queues, credential authorities, Tether gateways, Cerberus
  workspaces, repositories, and artifact systems are attached resources.
- Build creates immutable binaries and frontend assets; release binds them to
  configuration and registered adapters; run executes that release.
- Process-local state is disposable unless explicitly modeled as a durable
  attached resource.
- The daemon embeds its HTTP and MCP-facing servers and binds declared ports.
- Horizontal concurrency uses durable claims and a backend that supports the
  chosen process model; it does not rely on several uncoordinated SQLite
  writers.
- Startup is fast, shutdown is graceful, and in-flight attempts, sessions, and
  leases have recovery semantics.
- Development, test, and production exercise the same work, dispatch, policy,
  and persistence contracts.
- Logs go to stdout and stderr; domain audit and run history are structured
  stores.
- Migrations, reindexing, backup, restore, consistency checks, import, export,
  and repair run as one-off processes from the same release.

## Current strengths to preserve

The current implementation already contains many strong architectural choices:

- a durable task store with a validated finite-state machine
- normalized dependency and tag relationships
- separate run and session records
- explicit retry, cancellation, timeout, superseded-run, and operator-terminal
  handling
- transactional dispatch state and run-start facts in the primary database
- persistent run events, cost attribution, and source-aware estimates
- structured subtodos, deliverable requirements, checkpoints, comments, and
  AARs
- typed HITL workflows that reject required policies the substrate cannot
  enforce
- plans that do not dispatch as ordinary tasks
- task-scoped MCP loopbacks with curated worker surfaces
- launch-profile resolution and shared launch-library seams
- provider capability reporting and pre-dispatch validation
- deliberate separation of agent session state from task state
- local-first SQLite with a Postgres adapter path
- a dedicated writer, immediate transactions, and concurrency-focused tests
- per-run worktree isolation and preservation of dirty work
- official Anthropic and OpenAI SDKs behind an executor adapter
- shared `go-messaging`, `go-toolbroker`, `agentkit`, and plugin contracts
- mTLS and authority-aware federation design
- OpenTelemetry, SSE, health, scheduler diagnostics, and admin operations
- XDG-aware storage and Cerberus-managed service deployment
- HTTP, MCP, GUI, and Go service layers converging on common domain services

The target should clarify and consolidate these strengths rather than replace
Torque with a generic issue tracker or workflow engine.

## Architectural tensions to resolve

These are target-state design questions exposed by the current implementation,
not an implementation backlog.

### The task row carries too many authorities

One mutable record currently combines work intent, status, execution policy,
agent selection, system prompt, tools, permissions, environment, budgets,
quality gates, deliverables, relationships, source trust, and free-form
metadata. These fields do not all share the same author or mutation authority.
The target needs separate work, policy, authored-definition, and dispatch
contracts.

### Task status conflates several state machines

`todo`, `doing`, `review`, `blocked`, `paused`, `done`, and `archived` describe
a mixture of commitment, readiness, worker activity, human review, and
visibility. Derived readiness, attempt state, session state, gate state, and
work acceptance need separate identities.

### Current-state rows lack complete work history

Runs have events, but many task, plan, dependency, priority, assignment,
archive, and policy edits overwrite current rows without one canonical domain
event history. Hooks and comments provide partial evidence but do not replace
transactional work events.

### Kinds share one table but not one semantic model

Agent tasks, external work, waits, decisions, parent containers, plans,
internal automation, and issues share the task row and much of its FSM. This
keeps APIs compact but creates invalid combinations and special-case scheduler
logic. A shared work graph should support kind-specific invariants directly.

### Plans mix coordination data with prompt-driven control

Plan phases and refinements live in task metadata, while built-in planner and
orchestrator agents drive important behavior through prompts, comments, and
markers. Agents should contribute judgment; deterministic plan lifecycle and
event delivery belong to the substrate.

### `manual` is overloaded

The Boolean represents approval, scheduler eligibility, human work, container
behavior, and safety parking in different places. A declared dispatch policy
and readiness conditions are clearer and less error-prone.

### Execution bindings are assembled from mutable live state

Tasks and profile files can change between scheduling, retry, and launch.
Persisting an immutable dispatch binding would make attempts reproducible and
would separate the authored task from the effective execution release.

### Queue and authoritative state cross storage boundaries

The primary database, scheduler queue, telemetry queue, in-process worker pool,
and live registries have different atomicity and recovery properties. The
product needs one dispatch-intent and recovery contract across them.

### Several live registries are process-local

Session handles, steering poll opt-ins, reminders, approvals, hooks, and the
event bus rely on one process. That is valid under an explicit single-daemon
model, but alternate processes and restart recovery cannot pretend these
capabilities remain available without durable state or a daemon proxy.

### Stdio MCP can become a second runtime owner

The current MCP process opens the database, runs migrations, constructs an
agent manager, and may launch sessions even though it has no scheduler and
loses in-memory runtime handles on exit. The target makes MCP a client adapter
to the authoritative daemon instead of a second partial control plane.

### Project scoping is a scheduler stopgap

An environment-variable project allowlist protects against shared-database
cross-project dispatch but is not an ownership or authorization model. Work
scope belongs in every query, policy decision, and claim where isolation is
required.

### The Workspace term is overloaded

Current code uses workspace for a local session-state directory and
go-apppaths data instance, while the portfolio uses Workspace for a
Cerberus/Coder compute resource. The concepts need distinct names and handles.

### Required worktree isolation can degrade silently

Some worktree setup failures log and fall back to the shared checkout. When
isolation is declared as policy, fallback changes the safety contract and must
block instead.

### Agent lifecycle depends on prompt markers

Orchestrator completion can be inferred from specially authored comments and
session-complete prefixes. A model-generated marker is not a reliable
transaction boundary. Session and work completion need typed commands and
durable events.

### Permission policy is fragmented

Task tool allowlists, task permissions, project permissions, the Torque MCP
permission engine, provider-native permission modes, launch args, sandbox
configuration, and workspace policy are not one effective grant model. A
`bypassPermissions` profile or task-provided system prompt can otherwise carry
more authority than the work author should possess.

### Source trust is not authenticated authority

`source_type`, `source_ref`, `trust`, comment author strings, responder source,
and agent IDs are caller-supplied provenance. They need an authenticated actor
and authorization decision before they can govern execution or acceptance.

### Secret values can enter persistent configuration

Agent profiles accept literal API keys, tasks and templates accept arbitrary
environment maps, Mux tokens may be carried in command arguments, and the
API-key helper can read, refresh, and rewrite keychain credentials. The target
uses external secret references and narrow runtime grants.

### Tool-call audit is process-local

The default tool-broker audit sink is in memory. Tool calls and approval
decisions that can affect work or external systems need durable, task- and
attempt-correlated audit under policy.

### Messaging overlaps Tether

Torque now contains a durable broker, routing, steering, federation, peer
identity, and mTLS surface. Task-scoped messaging is legitimate; a second
portfolio-wide messaging and federation platform is not the desired composed
boundary. Generic mechanics should live in shared contracts or Tether while
Torque keeps task semantics and a standalone local adapter.

### Events have multiple sources of truth

Database transition hooks, run events, direct SSE emissions, the in-process
event bus, canonical-event sketches, comments, and message envelopes overlap.
The target needs transactional domain events, live projections, telemetry, and
messages as clearly distinct layers.

### Artifact custody is too open-ended

Artifacts may carry inline content, URLs, or file paths without one authority,
digest, verification, or retention contract. Evidence references should be the
default; bounded Torque-native content should be an explicit class.

### AAR submission relies on agent compliance

The current protocol asks every agent to file exactly one structured report.
Known run facts should be harness-authored, with agent reflection attached as
optional authored content rather than serving as the only execution record.

### Backup is deployment-specific

The current backup loop snapshots the main SQLite database and relies on
Cerberus/launchd for keepalive. It does not express the complete recovery
contract for the scheduler queue, Postgres, external evidence, configuration,
or in-flight claims. Backup should be a product operation scheduled externally.

### SQLite and Postgres are not merely interchangeable DSNs

SQLite currently requires significant writer, queue, and concurrency policy,
while Postgres has different locking and migration behavior. Both can remain,
but advertised parity needs shared semantic tests and an explicit process
model.

### Plugin executor contracts have drifted

The plugin-facing executor and runtime executor duplicate similar concepts
with different context, job, result, capability, and limit shapes. Broad host
services and non-transactional registration also make plugin authority larger
than necessary.

### Feature flags affect protocol shape

Projects, sprints, epics, and collections are stored even when hidden, while
MCP tools register only at process startup. Enabled modules, data visibility,
and protocol capability discovery need one coherent contract.

### Budget sentinels obscure intent

`null`, `-1`, `0`, and positive values currently encode inheritance,
unlimited, explicit zero, and a value differently by field. A typed budget
policy should represent `inherit`, `unlimited`, and `limit` without magic
numbers and should record the effective value in each dispatch binding.

### Compatibility vocabulary remains visible

Clockwork, Fragments Engine, Volon, Mux, Vanta, and current Torque names appear
across IDs, environment variables, docs, code comments, and deployment
resources. Compatibility aliases may remain, but Torque needs one canonical
public vocabulary so new integrations do not deepen historical coupling.

## Boundary guidance

When deciding whether a capability belongs in Torque, use these tests.

It belongs in Torque when it primarily:

- creates or governs managed work and its relationships
- evaluates work readiness and scheduling policy
- binds and dispatches an execution attempt
- controls task-level retry, cancellation, timeout, review, and acceptance
- correlates runs, gates, evidence, comments, cost, and audit with work
- operates Torque's own daemon, store, scheduler, and adapters

It probably belongs elsewhere when it primarily:

- defines or executes a reusable business workflow language
- manages interactive agents, conversations, or teams
- provides general LLM/MCP gateway or cross-application messaging services
- provisions infrastructure or Coder workspaces
- stores repository content, large artifacts, or canonical application data
- manages provider secrets or identity-provider lifecycle
- turns execution feedback into general memory without explicit promotion

If the answer is mixed, keep authority in the owning system and compose through
an immutable reference, normalized handle, typed command, event, evidence
record, or narrow adapter.

## Questions for the next architecture session

1. Which Torque entities are natively authoritative work records, and which
   are always references or projections of external project systems?
2. What is the smallest stable `WorkItem` contract, and which current kinds
   require distinct domain records or lifecycle profiles?
3. What exact work lifecycle replaces or clarifies the current task FSM, and
   how is derived readiness surfaced?
4. Which plan, phase, epic, sprint, and collection semantics are core to
   Torque rather than optional project-management convenience?
5. What immutable revision of a task is bound to an attempt, and which edits
   require a new dispatch binding?
6. What durable event model provides complete work history while keeping
   high-volume run telemetry separate?
7. What queue, claim, fencing, and recovery contract works for both local
   SQLite and multi-process Postgres deployments?
8. Is `torque serve` always the authoritative runtime, with CLI and MCP as
   clients, or is a separately fenced embedded mode also a product contract?
9. Which task-agent mechanics move into `go-agent-wrapper`, and which session
   behavior remains necessarily Torque-specific?
10. Which built-in planner, orchestrator, reviewer, and supervisor behaviors
    remain product features, and which become externally owned launch or
    process definitions?
11. What is the common effect-grant model across task tools, project policy,
    provider permission modes, sandboxing, MCP gateways, and Coder workspaces?
12. Which comments and bounded artifacts are canonical Torque records, and
    which should always be external evidence references?
13. What task-scoped messaging remains inside standalone Torque, and which
    federation, inbox, and delivery capabilities delegate to Tether when
    composed?
14. What is the exact contract for dispatching a Hadron workflow from a Torque
    work item without duplicating retries, gates, or completion authority?
15. Does every isolated Torque run acquire a Coder workspace by default, or is
    local execution a peer target selected by policy?
16. What terminology replaces local `workspace_dir` and the go-apppaths
    workspace selector so `Workspace` means only the Cerberus/Coder resource?
17. Which identity provider and service-local capability model protects HTTP,
    MCP, GUI, task loopbacks, and break-glass operations?
18. What complete backup and restore manifest covers work, dispatch, policy,
    messages, sessions, queues, and externally referenced evidence?
19. Which Torque-specific extension contracts are stable enough for the new
    Hollis Labs plugin SDK?
20. Which Clockwork, Fragments, Volon, Mux, and legacy environment names are
    permanent compatibility contracts versus transitional aliases?

These questions refine the target without changing the central direction:
Torque is the authority for coordinated work and task-bound dispatch, not the
owner of every definition, runtime, message, workspace, or artifact involved
in completing that work.
