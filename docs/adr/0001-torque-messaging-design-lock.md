# ADR-0001 — Torque Messaging: Design Lock (Steering + Federation Contracts)

**Status:** Accepted
**Date:** 2026-05-18
**Supersedes:** —
**Plan:** CW-20260518-0038 (Torque Messaging — federated agent/user communication)
**Task:** CW-20260518-0039 (Design Lock, phase ph-1)
**Source design:** `agent-os/docs/torque-messaging-design.md`

---

## Context

Torque needs a messaging system so long-running agents (orchestrators) can send
operational messages, surface blockers, relay subagent detail, and coordinate
with one another — and so users can steer, guide, redirect, and ask for status.
The system must be **federated and email-like** (internal vs. external is only
addressing / routing), must work **standalone** (a Torque-only install with no
peer daemons), and must be **reusable by any app**.

A design study synthesizing three parallel code reviews (Tether messaging,
Nanite messaging, and the `go-messaging` library against Torque's current
state) found that this is **~75–80% already built**. Torque already depends on
`github.com/hollis-labs/go-messaging v0.2.0` and ships a working stack:

- `internal/messaging.Store` — a durable SQLite `Store` (`messages` +
  `message_deliveries`, atomic delivery, per-recipient lifecycle, `Subscribe`).
- `internal/broker.Broker` — a typed layer over the `Store` with kind-aware
  helpers, payload validation, a 256 KiB size cap, and SSE publishing.
- `internal/runtime/reactor` — a deterministic envelope→action dispatcher.
- Surfaces: `torque_broker_*` MCP tools, `/api/v1/broker/*` and
  `/api/v1/messages/*` HTTP, an SSE `/messages/subscribe` stream.

The remaining ~20–25% is net-new work across five later phases of plan
CW-20260518-0038. Before that work starts, the **contracts** the rest of the
plan builds against must be locked so producers and consumers do not drift.

This ADR records the decisions already made (see plan CW-20260518-0038) and
**locks four contracts**: the envelope `Kind` set, the addressing scheme, the
steering-bridge contract, and the authority-routing decorator contract. Two
companion decisions remain open and are explicitly delegated below.

---

## Decision

### 1. The contract layer stays `go-messaging` — no schema fork

`go-messaging` remains the single shared contract. It already satisfies
"reusable by any app": it is a portfolio-shared contract (not a service) with
`Envelope`, typed `Address`, a closed `Kind` enum, `Store` / `Dispatcher`
interfaces, a contract test suite (`messagingtest.RunContract`), and an
in-memory reference store. Torque keeps its own SQLite `Store`; any other app
implements its own against the same interface. **No new foundation is built,
and no app forks the envelope schema.** Federation is achieved by routing
(decision 4), never by diverging the wire format.

---

### 2. Envelope `Kind` set — LOCKED (closed enum)

The envelope `Kind` field is a **closed enum**. Its canonical definition is the
`go-messaging` v0.2.0 `Kind` constants. Torque does not add, rename, or
reinterpret kinds; the messaging layer routes by kind, and *semantics* belong
to the application layer (the broker and reactor).

| Wire value      | Constant              | Direction        | Semantics                                                                      |
|-----------------|-----------------------|------------------|--------------------------------------------------------------------------------|
| `request`       | `MsgKindRequest`      | sender → target  | Initiates a work item or question. Expects a `response`.                       |
| `response`      | `MsgKindResponse`     | target → sender  | Carries the answer to a `request`. Sets `in_reply_to`.                         |
| `notice`        | `MsgKindNotice`       | sender → target  | One-way informational message. No reply expected.                              |
| `status_update` | `MsgKindStatusUpdate` | agent → any      | Periodic progress / state report from an agent.                                |
| `handoff`       | `MsgKindHandoff`      | agent → agent    | Transfers ownership of a work unit across logical agents.                      |
| `escalation`    | `MsgKindEscalation`   | agent → user     | Signals that human or elevated-priority attention is needed.                   |

Notes that are part of the lock:

- The "reply" kind is `response` on the wire (`MsgKindResponse`). Torque uses
  the wire string `response` everywhere; "reply" is only a human term.
- Any new message category MUST be modeled with an existing kind plus an
  application-level discriminator (the broker's typed payloads, or the
  `Channel` field), **not** a new `Kind`. Extending the enum requires a new ADR
  and a `go-messaging` release that every peer adopts in lockstep.

The `Envelope` shape is also locked to the `go-messaging` v0.2.0 struct: `ID`,
`Kind`, `Channel`, `From`, `To`, `ThreadID`, `InReplyTo`, `Payload`,
`ContentType`, `Metadata`, `CreatedAt`, `DeliveredAt`, `ConsumedAt`. `Channel`
is an opaque UX-layer pass-through; the shared library never interprets it.

**Torque broker payloads.** The broker layers typed payload bodies on specific
kinds — `EscalationPayload` (`severity` ∈ `info|warn|error|critical`, `reason`,
`context`), `HandoffPayload` (`task_id`, `from`, `to`, `note`), and
`StatusPayload` (`state`, `progress`, `note`). These are application-level
conventions carried in `Payload`; they do not change the `Kind` enum. Every
payload is capped at 256 KiB (`broker.MaxPayloadBytes`).

---

### 3. Addressing scheme — LOCKED

Every sender and recipient is a canonical `go-messaging` URN:

```
msg://<kind>/<authority>/<id>[/<subid>]
```

| Segment     | Definition                                                                  |
|-------------|------------------------------------------------------------------------------|
| `kind`      | Address kind — closed enum: `agent`, `user`, `service`, `session`, `workflow`. |
| `authority` | The routing namespace / owner — the email-style "domain" (see decision 4).   |
| `id`        | Stable entity identifier within the authority.                               |
| `subid`     | Optional sub-entity (e.g. a session ID within an agent identity).            |

Locked rules:

- `authority` is the **single routing seam** for federation. It is the only
  field that decides internal vs. external (decision 4).
- The address `kind` enum (`agent`/`user`/`service`/`session`/`workflow`) is
  closed and distinct from the envelope `Kind` enum in decision 2.
- URNs are validated with `go-messaging`'s `ParseURN`. Malformed structure or
  an unknown address kind is rejected at write time as an invalid request.
- Conventional Torque addresses:

  | Entity                          | URN                                          |
  |---------------------------------|----------------------------------------------|
  | A Torque user                   | `msg://user/<authority>/<user-id>`           |
  | An orchestrator agent           | `msg://agent/<authority>/<agent-id>`         |
  | A running session               | `msg://session/<authority>/<session-id>`     |
  | A worker task                   | `msg://service/<authority>/<task-id>`        |

- Each Torque install has **one configured local authority identifier**
  (default: `torque`). The set of authorities a given install treats as local
  is held by the routing decorator (decision 4); a standalone install has
  exactly one entry — its own.

---

### 4. Federation = an authority-routing `Store` decorator — LOCKED

Federation is delivered by **routing**, not a schema fork (decision 1). The
mechanism is an **authority-routing `Store` decorator**: a type that implements
the `go-messaging` `Store` interface and dispatches each call by the
`authority` segment of the relevant `Address`.

**Decorator contract (locked):**

- The decorator implements `go-messaging.Store` in full — `Send`, `Get`,
  `Inbox`, `Thread`, `Consume`, `Cancel`, `Subscribe` — and is substitutable
  anywhere a `Store` is expected. It adds routing; it does not change the
  contract or the lifecycle semantics (`Inbox` stays destructive /
  exactly-once-per-recipient; `Thread` stays read-only; `Subscribe` stays
  best-effort live-only).
- It holds two things: the **set of local authorities** and a
  **foreign-route registry** mapping a foreign `authority` → a transport
  endpoint.
- Routing rule: resolve the governing `Address` authority for the call (the
  recipient `To` for `Send`/`Inbox`/`Subscribe`; the owning authority for
  `Get`/`Thread`/`Consume`/`Cancel`).
  - **Local authority** → delegate to Torque's SQLite `Store`.
  - **Foreign authority with a registered route** → delegate to an HTTP-backed
    `Store` for that route. The HTTP `Store` generalizes the proven
    `go-agentmux-client` `httpStore` pattern.
  - **Foreign authority with no route** → reject as an unroutable address; the
    error surfaces to the caller, the envelope is not silently dropped.
- "Internal vs. external" is **exactly** this one question: *is the
  authority in the local set?* There is no separate internal/external schema,
  table, or kind.

**Standalone guarantee (locked):** a Torque-only install registers **no foreign
routes**. The decorator's registry is empty, every authority resolves local,
and messaging works fully with zero extra configuration. Federation is purely
additive — wiring it on never changes standalone behavior.

**Library-extraction intent.** The decorator is generic (composition over the
`Store` interface). Plan CW-20260518-0038 phase ph-5 promotes it into
`go-messaging` (or a thin sibling) so every app gets federation for free. This
ADR locks the decorator's *contract*; the *package home* is settled in ph-5 and
does not affect the contract.

**Cross-authority auth is OUT OF SCOPE here.** The decorator + a secured
foreign-authority hop is the approved approach, but the concrete auth mechanism
(mTLS / signed envelopes / per-authority tokens) is specified by task **M2
(`CW-20260518-0040`)** before the Federation phase (ph-4) begins. Until M2
lands, the foreign-route registry carries no credentials and no install ships
foreign routes.

---

### 5. Steering bridge: inject-at-turn-boundary — LOCKED

The "steering bridge" is the missing link between the envelope model and live
agent sessions. Today `internal/runtime/agent.SendTurn` delivers text *into* a
running agent process (stdin / JSON-RPC) but is **not** wired to the envelope
model. This ADR locks how the two connect.

**Direction — agent → user.** Operational messages and blockers travel as
envelopes into a user inbox. This already largely works via the broker
(`escalation`, `status_update`, `notice`). No new contract is locked here
beyond decision 2.

**Direction — user → agent (the steering bridge).** An envelope addressed to a
running agent / session is delivered into that agent's loop. The locked
delivery model is **inject-at-turn-boundary**:

- When a steered agent's current turn ends, its inbox is drained for envelopes
  addressed to it, and the matching envelopes are injected as the input of its
  **next turn** via `SendTurn`.
- Delivery uses the existing `Store.Inbox` semantics: it is destructive and
  exactly-once-per-recipient, so an envelope injected into a turn is not
  re-injected.
- The bridge does **not** pre-empt or interrupt a turn already in progress.

**Opt-in polling.** An agent that is *actively communicating* may opt into
polling its inbox **between tool calls** (a finer-grained boundary than the
turn boundary) to pick up steering sooner. Polling is opt-in per agent; the
default remains turn-boundary injection. Both paths use the same `Store.Inbox`
delivery semantics — polling only changes *when* the inbox is drained, never
*how* an envelope is consumed.

**Interrupt — DEFERRED.** A delivery mode that pre-empts the current turn is
explicitly **deferred** until a concrete use case arises. It is not designed,
not stubbed, and not part of this plan. Revisiting it requires a new ADR.

**Reactor coverage.** Torque's `internal/runtime/reactor` deterministically
routes envelopes to in-process actions. Today it covers four kinds:
`escalation` → HITL checkpoint, `status_update` (state `blocked`) → pause &
block the task, `request` → peer fanout, `handoff` → reassignment; every other
kind is `noop`+log. The steering bridge requires the reactor to additionally
route **`notice`** and **`response`** envelopes (so user-directed steering
notices and agent replies reach a running agent). Adding that coverage is
locked as in-scope for the Steering & Reactor phase (ph-2); this ADR does not
fix the reactor's internal dispatch shape (it remains a struct + switch until
shapes settle).

---

## Consequences

**Positive**

- Producers and consumers across Torque (and later Nanite and Tether) share one
  envelope schema, one addressing scheme, and one `Store` contract. No drift.
- "Federated," "email-like," and "works without Tether" are delivered by a
  *single* mechanism — the authority-routing decorator — rather than three
  separate features.
- Standalone Torque is unaffected by federation: an empty route registry is the
  zero-config default.
- The steering bridge has an unambiguous, testable delivery model; later phases
  build a known target instead of re-deciding it.
- The decorator is generic enough to be promoted into `go-messaging`, giving
  every portfolio app federation without bespoke code.

**Negative / accepted tradeoffs**

- Inject-at-turn-boundary means a steering message can wait until the current
  turn (or, with polling, the current tool call) finishes. For a long,
  non-communicating turn this latency can be significant. Accepted: the
  interrupt path is deferred precisely because no use case yet justifies the
  complexity of pre-empting a turn safely.
- The envelope `Kind` enum is closed and changing it is a cross-repo,
  lockstep-release operation. Accepted: this is the cost of a stable federated
  contract; application-level discrimination (typed payloads, `Channel`) absorbs
  most extension needs.
- Cross-authority auth is unresolved until M2. Until then no install can route
  to a foreign authority. Accepted: federation (ph-4) is gated on M2 anyway.
- A foreign authority with no registered route is a hard error at send time.
  Accepted: silent drop is worse; callers get a clear, actionable failure.

**Follow-ups / delegated decisions**

- **M2 (`CW-20260518-0040`)** — specify the cross-authority auth mechanism
  before phase ph-4.
- **Phase ph-5 (`CW-20260518-0049`)** — decide the package home of the
  authority-routing decorator (promote into `go-messaging` or a sibling lib).
  This ADR locks the decorator's contract, not its location.
- **Phase ph-2** — extend reactor coverage to `notice` and `response`; wire the
  currently-dead `stuck.Probe` trigger.

---

## References

- Design study: `agent-os/docs/torque-messaging-design.md`
- Plan: CW-20260518-0038 — phases ph-1 … ph-6
- Library: `github.com/hollis-labs/go-messaging` v0.2.0 —
  `messaging.go` (Envelope, `Kind`, `AddressKind`), `urn.go` (`ParseURN`),
  `store.go` (`Store` / `Dispatcher`), `messagingtest/` (contract suite).
- Torque: `internal/messaging/sqlstore.go`, `internal/broker/broker.go`,
  `internal/runtime/reactor/dispatcher.go`, `internal/runtime/agent/send_turn.go`,
  `internal/runtime/stuck/stuck.go`.
- Federation transport template: `go-agentmux-client` `httpStore`.
- Prior art: Tether `docs/adr/0023-message-routing-contract.md`.
