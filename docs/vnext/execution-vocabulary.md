# Execution vocabulary and the shared-capability map

**Status: analysis, 2026-09-07. Portfolio-scoped, written here because Torque
vNext phase 1 needs it. A candidate for promotion out of this repo.**

Three words — *scheduler*, *queue*, *runner* — are doing the work of six
distinct concerns, in this portfolio and in the industry generally. The
confusion is not local sloppiness; it is inherited. This document separates the
concerns, checks the naming against how established systems name them, and maps
the `hollis-labs` libraries onto the result.

Two findings fall out, at the end.

---

## 1. Six concerns

| # | Concern | The question it answers |
|---|---|---|
| 1 | **Activation** | What makes us *look* at work right now? |
| 2 | **Evaluation** | Given everything that just changed, what is *eligible*? |
| 3 | **Queue** | What is admitted and waiting, in what order, routed to whom? |
| 4 | **Assignment** | Which single worker owns this unit, provably once? |
| 5 | **Worker** | The process that takes assigned work and runs it. |
| 6 | **Executor** | *How* one unit actually executes. |

Time-driven and policy-driven are not two systems. They are **concern 1 versus
concern 2** — a trigger versus an eligibility rule — and both feed the same 3–6.
A cron kickoff and a dependency being satisfied are the same kind of event: *a
reason to re-evaluate*. That is the seam.

---

## 2. How established systems name these

Not law, but the range a reader arrives with.

| System | 1 Activation | 2 Evaluation | 3 Queue | 4 Assignment | 5 Worker | 6 Executor |
|---|---|---|---|---|---|---|
| **Temporal** | Schedule | (in the workflow) | **Task Queue** — a *routing channel* workers poll, not storage you manage | task delivery + heartbeat | Worker | Activity |
| **Airflow** | schedule / Sensor / deferrable Trigger | **Scheduler** | `queue` — a Celery *routing label* | task instance | worker | **Executor** (Local/Celery/Kubernetes) |
| **Kubernetes** | controllers | Admission controllers | — | **Scheduler** = *binding to a node* | kubelet | container runtime |
| **Nomad** | — | **Evaluation** — "something changed, recompute" | — | **Allocation** | client | task driver |
| **Celery** | beat | — | queue (on a broker) | — | Worker | pool |
| **River / Sidekiq** | periodic jobs | — | named queue + priority | — | Worker | — |
| **CI (Actions/GitLab)** | trigger | — | — | — | **Runner** | — |

### The trap words

**"Scheduler" is the worst offender.** It legitimately means *time triggering*
(Temporal, cron, `go-scheduler`), *eligibility evaluation* (Airflow, Nomad), and
*placement* (Kubernetes) — three different concerns, all correct usages. Any
vocabulary built on it inherits the ambiguity. **Do not name a component
"scheduler."**

**"Queue" is the second.** Chrispian's framing — *"the data at rest is the task
queue but the Task Queue Engine would be where it's executed"* — is exactly the
fault line. Temporal's task queue is a **routing channel with no storage
semantics you touch**; Celery's is broker storage; Airflow's is a routing label.
The fix is not a different word, it is a **restriction**: a queue is data at rest
plus a routing name, and **nothing is ever called a queue engine**. What enters
it is admission; what takes from it is a dispatcher or a worker pool.

**"Runner" is CI-flavoured** and in this portfolio `go-runner` already means
something more specific — the spawn-and-parse substrate. Prefer **Worker** for
the process and leave `runner` to the library that owns it.

---

## 3. The proposed vocabulary

| Term | Definition | Rejected alternative |
|---|---|---|
| **Activation** | A reason to consider work: time, event, manual, external, or a state change elsewhere. Produces an evaluation request, never work. | *Scheduler* (ambiguous), *Trigger* (fine, but Hadron already says activation) |
| **Evaluation** | Recompute eligibility over the current graph and policy. Emits eligible units **and the reasons the rest are not**. | *Readiness check* (understates it), *Scheduler* (ambiguous) |
| **Queue** | Admitted work at rest, ordered, with a routing name. A noun. Never an engine. | *Task queue* (imports Temporal's routing-only sense) |
| **Assignment** | One unit bound to one worker, held by a lease with a fencing guard. The record; **claim** is the operation that creates it. | *Allocation* (Nomad-specific), *Binding* (collides with `DispatchBinding`) |
| **Worker** | The process that takes assignments and runs them. | *Runner* (CI-flavoured; name taken) |
| **Executor** | The adapter for *how* one unit executes — agent, workflow, command, external, human. Already ruled: the **actor**, not the vehicle. | — |

Time-driven and policy-driven both land at **Activation → Evaluation**. A cron
fire and a dependency clearing are the same event class.

---

## 4. Where the hollis-labs libraries actually sit

Read in-tree 2026-09-07.

| Concern | Library | What it owns | Verdict |
|---|---|---|---|
| 1 Activation | **go-scheduler** v0.2.0, 949 LOC | schedules, durable fire identity, CAS claim, lease + fencing, retry/backoff, exhaustion, restart recovery | Correctly scoped. Time only. Nanite and Hadron consume it; Torque does not. |
| 2 Evaluation | **go-workflow** — `ReadyRule` (six join semantics), `SchedulerAdmissionPolicy`, `ReadyQueuePolicy`, node `PolicyRequirement`, `ExecutionTargetRequirements`, `RetryAuthorizer`, `ReuseAuthorizer` | full eligibility + admission — **over a pinned, immutable compiled plan** | **Gap for mutable graphs.** See §5. |
| 3 Queue | **go-queue** v0.1.x — named queues, priority across queues, retry, per-job max attempts, SQLite/memory/noop drivers, a polling `Worker` | general background jobs | Fine. Also: `go-workflow` has its own internal `ready_queue`, at a different level. |
| 4 Assignment | **go-scheduler** `FireClaim`/`TransitionFire`; **go-workflow** `ClaimNodeRequest`/`ClaimResult`/`RenewLeaseRequest`; **Torque** its own | claim, lease, fence, CAS release | **Implemented three times.** See §5. |
| 5 Worker | `go-queue`'s `Worker`; `go-workflow` expects the host to supply one | | no shared contract |
| 6 Executor | **go-runner** v0.7.0 — spawn under sandbox, parse via a provider adapter, emit raw process/stream events, plus process-lifetime supervision | the vehicle beneath an agent executor | Correctly bounded. Its README is explicit: *"This library does not know what an FSM transition is."* |

### How much policy the workflow engine actually has

More than Torque does. `runtime/scheduler_resources.go` enforces **six
independent admission dimensions** — `worker`, `run`, `effect`, `capability`,
`concurrency_key`, `fan_out` — through `AdmitNodeRequest`, which atomically
acquires a node claim *and* all resource requirements or records a diagnostic
waiter acquiring neither, and reports exactly which resources blocked it.
`ReadyQueuePolicy` lets a host reorder or sub-select the FIFO candidate list.
`ReadyRule` covers `all_success`, `all_done`, `one_failed`, `all_failed`,
`none_failed`, `always`.

That is a **better-articulated policy vocabulary than Torque has**, and Torque
should adopt the vocabulary even where it cannot adopt the code.

---

## 5. The two findings

### A. Claim / lease / fence exists three times

The same primitive — *atomically take exclusive ownership of an identified unit,
with a lease, an expected-state guard, a fencing token, and CAS-safe
release/renew* — is implemented independently in three places:

```text
go-scheduler   FireClaim{FireID, ExpectedStatus, ExpectedAttempt,
                         ExpectedFiredAt, ClaimedAt, ClaimExpiresAt}
               TransitionFire CAS fences a stale owner via ClaimedAt

go-workflow    ClaimNodeRequest{InvocationID, ExpectedClaimGeneration,
                                Owner, Token, IdempotencyKey, Now, LeaseUntil}
               ClaimResult{Acquired, Replayed, Lease} + RenewLeaseRequest

Torque         its own, inside internal/runtime/scheduler
```

Same primitive, three vocabularies, three sets of tests. It is the hardest thing
in this stack to get right and the most dangerous to get wrong, which makes it
the strongest candidate for a shared primitive.

**But not a merge.** The two library shapes guard on genuinely different state:
`ExpectedClaimGeneration` (a monotonic counter) versus
`ExpectedStatus + ExpectedAttempt + ExpectedFiredAt` (a compound state tuple).
Extracting these into one contract needs a real design pass that decides which
guard is general, not a mechanical unification. Torque writing a *fourth* one is
the outcome worth avoiding.

### B. Nothing evaluates a **mutable** work graph

This is the direct answer to *"is the policy-driven system just Torque's
workflow engine + scheduling?"*

**Almost — and the "almost" is the entire product.**

| | go-workflow | Torque |
|---|---|---|
| Graph | one compiled plan, pinned digest, frozen kind registry | open, mutable, unbounded |
| Lifetime | one run | no enclosing run at all |
| Shape changes mid-flight | **no**, by design | constantly — work is added while other work runs, dependencies are edited |
| Source of nodes | *"load nodes from that pinned plan rather than a mutable source"* (its own adoption guide) | a living board |

Pinning is not an incidental restriction — it is **what makes go-workflow's
recovery, replay and memoization guarantees sound**. Asking it to evaluate a
mutable graph would break the property that makes it worth embedding. So this is
a real gap, not a missing feature request.

Nomad has the word for what is missing: an **Evaluation** — *something changed,
recompute what should now run.* No `hollis-labs` library provides it.

That is why Torque's 4,828 LOC exists, and it is the honest justification for
Torque continuing to own something in this space. It is also, eventually, the
best candidate in Torque for extraction — after it is proven, not before.

---

## 5b. The general rule underneath: at rest is not the same noun as at runtime

The queue problem — *"the data at rest is the task queue but the Task Queue
Engine would be where it's executed"* — is one instance of something wider, and
the wider version is the more useful rule.

**An agent is the clearest case.** At rest it is not a thing at all: it is a
profile, an extends cascade, templates, slots, skills, prompts and
provider-keyed settings, scattered across a catalog. It only becomes an agent
when something assembles those pieces into a boot directory and starts a
process. Same word, two states, different lifecycles, different owners,
different authorities.

The portfolio's **newest libraries already do this correctly**, with three nouns
each:

| Domain | At rest | Materialized | Running |
|---|---|---|---|
| Schedule | schedule row | **fire** (`DeriveFireID`) | attempt |
| Workflow | definition source | **compiled plan** (pinned digest) | run → node invocation |
| Agent (Cairn) | profile + parts | **boot directory** (+ boot report) | session |
| Work (vNext) | work item | **dispatch binding** | attempt |
| Policy (vNext) | authored `ExecutionPolicy` | **effect grants** | enforcement at the boundary |
| Queue | queued rows | **assignment** (claim + lease) | worker executing |

`go-scheduler` and `go-workflow` — the two most recently designed — did not need
to be told this. `Schedule → Fire → Attempt` and `Definition → Plan → Run` are
already three nouns. Cairn does it too, and enforces it by refusing to own the
third column at all: it *"does not launch, monitor, track, control or grant
authority to agents."* `agent-setup` states the principle in prose: **"A role is
not a launch mode."**

What is missing is naming the discipline, so the older code stops collapsing it.

### The rule

> **Three nouns, not one: the composition at rest, the materialization, and the
> running instance. The materialization step must produce a recorded artifact
> with a digest.**

### Why composition is what makes it possible

If the at-rest form is a **monolith**, materialization is invisible — you just
*use* the thing, there is no step, and there is nothing to record. If the at-rest
form is **pieces**, materialization becomes a real operation with real inputs and
a real output: a boot report, a compiled plan, a dispatch binding. That output
can be digested, recorded, diffed, replayed and audited.

So composition is not only about reuse. **It is what makes the materialization
step legible** — and a step you cannot see is a step you cannot reproduce or
explain. That is the same reason vNext records a digest for every materialization
in the `DispatchBinding`: resolved policy, resolved launch, boot report.

### Where vNext currently breaks its own rule

One gap. Every materialization in the design has a name except the compiled
configuration: the precedence chain in §5 of
[architecture.md](architecture.md#5-configuration) resolves operator → scope →
policy → work item → attempt into an effective decision, and **that decision has
no noun and no digest**. It should have both, for the same reason the dispatch
binding does — "why was this run allowed to do that?" is a question about the
compiled config, and today it would only be answerable by re-running the
resolution.

## 6. What this means for Torque vNext

1. **Stop using "scheduler" as a component name.** `internal/runtime/scheduler`
   currently spans concerns 1–5. Split it along the six above and name the parts
   for what they do.
2. **Activation is `go-scheduler`'s.** Torque has no timed activation today;
   when it needs one, consume the library rather than growing a ticker. Nanite's
   full-replace migration is the pattern.
3. **Evaluation is Torque's**, and is the thing worth being good at. Adopt
   go-workflow's admission vocabulary — the six resource dimensions, the
   ready-candidate/selection split, the reasons-for-blocked diagnostic — even
   where the code cannot be shared.
4. **A queue is data at rest.** Torque currently has **two**:
   `internal/runtime/queue` (its own 267-LOC SQLite job queue, consumed by the
   scheduler) and `internal/persistence/writequeue` (on `go-queue`, for
   telemetry). One of those duplicates the shared library. Phase 3 moves
   authoritative queueing into the primary store for the commit boundary;
   `go-queue` keeps the lossy telemetry lane.
5. **Do not write a fourth claim implementation.** Either consume one of the two
   existing ones or contribute the shared primitive — and that choice is a
   portfolio decision, not Torque's alone.
6. **Name the compiled configuration.** §5b: it is the one materialization in
   the design with no noun and no digest.
7. **`go-runner` is already correct.** It is the vehicle beneath
   `executor.agent`, and its boundary statement is the model for how the rest of
   this should be scoped.

## 7. Open

- Is the shared claim/lease/fence primitive worth extracting, and which guard
  shape is the general one? Needs Nanite, Hadron and Torque in the room.
- Does mutable-graph **Evaluation** eventually become a shared library, or stay
  Torque's product? Prove it in Torque first.
- Does `go-workflow` want the `Activation` seam formalized? It already expects a
  `wait.ActivationScheduler` from its host, and both Hadron and Torque would
  satisfy it with `go-scheduler`.
- Hadron is pinned to `go-scheduler v0.1.1` and has not taken v0.2.0's durable
  fire contract. Hadron's call, noted here because it affects any shared-claim
  conversation.
