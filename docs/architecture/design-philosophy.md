# Fragments Engine — Design Philosophy

## Status: Living Document

## Core Principle: Agent-First System Design

The choices we make in system architecture aren't just programming or systems design — they're optimizing for agent discovery and progressive context. The goal is to make the correct path light up like the sun for any agent entering the system.

This is what makes Fragments Engine "agent-first": the system itself is designed so that an agent can discover what it needs, when it needs it, without requiring the full picture upfront.

## Guiding Principles

### 1. Composition Over Coupling
Every service has a clear boundary. Volon = execution/orchestration. Vanta Conduit = memory/context. Cerberus = infrastructure. Hadron = automation. Mentat = meta-agent/planning. Services compose through APIs, not shared state. Tight coupling is a code smell we actively hunt.

**Why this matters for agents:** Loose coupling reduces the decision surface. An agent working on a Cerberus task doesn't need to understand Volon internals. It just needs to know the Cerberus API. Fewer decisions = fewer errors.

### 2. Tools Provide Functionality, Not Process
Our tools don't prescribe how to work. They provide capabilities. This is what lets us (and anyone using the tools) build anything. The process lives in the harness (agentrc, boot profiles, sprint workflows), not in the tools themselves.

**Why this matters for agents:** An agent can use `volon_task_create` without knowing our sprint methodology. The tool just creates a task. The process layer (which tasks to create, when, in what order) is a separate concern.

### 3. Progressive Context Discovery
Agents don't need everything at once. They boot with a profile, get their immediate task context, and discover more as needed through ContextBroker, Vanta Conduit, and the stream. Information is layered: boot context → task context → discovery on demand.

**Why this matters for agents:** Small initial context = less drift. Agents stay focused on their immediate scope. When they need more, they pull it through well-defined channels (MCP tools, context broker).

### 4. The Harness Keeps Everyone Honest
There are too many steps in any non-trivial workflow for anyone (human or agent) to remember reliably. The harness (agentrc boot profiles, hooks, validation, enforcement at the API level) exists to codify decisions so they don't need to be remembered. This isn't bureaucracy — it's reliability.

**Why this matters for agents:** Agents don't skip steps because the system won't let them. A task can't be marked done without a completion summary. A service can't be stopped without a reason. These guardrails are baked into the API, not the prompt.

### 5. Canonical Objects as Single Source of Truth
State lives in the database, accessed through APIs. File-based state is a read-only cache. If an agent can't write to the DB via an allowed method (MCP, CLI, API), it stops and asks. No silent failures, no file-based workarounds.

**Why this matters for agents:** One place to look for truth. No ambiguity about whether the file or the DB is correct. The DB is always correct.

### 6. Streams as Universal Interface
Every interaction is a conversation with the system. Comments, chat, A2A, A2U, alerts, handoffs, agent output — all are messages in scoped streams. The context determines the specifics, but the primitive is always the same.

**Why this matters for agents:** One interface to learn. Post a message, read a stream. Whether it's a task comment or an A2A coordination message, the mechanics are identical.

## Design Patterns

### Reducing Agent Decision Surface
Every architectural choice should be evaluated through the lens: "does this reduce the number of decisions an agent has to make?" Fewer decisions = faster execution, fewer errors, tighter context.

Examples:
- Service ownership boundaries eliminate "where does this go?" decisions
- API-level validation eliminates "did I fill this out correctly?" decisions
- Auto-generated streams eliminate "should I create a channel?" decisions
- Boot profiles eliminate "how do I start working?" decisions

### Dogfooding the Agent Experience
We use Mentat (this conversation pattern) to plan, decide, and orchestrate. The friction we feel is the friction agents feel. When something requires too many steps, too much context, or too much memory — that's a signal to automate or simplify.

### Reactive Intelligence in the Stream
Special Agents observe streams for specific patterns (architectural decisions, task candidates, convention changes) and dispatch specialist agents to act. This is zero-overhead capture: participants don't change behavior, the system extracts value automatically.

Pattern: Stream event → Observer scans → Match found → Pointer created → Specialist dispatched → Artifact produced → Notification in stream

## Anti-Patterns

- **Per-repo databases** — violates single source of truth
- **File-based state as primary** — leads to stale data and split-brain
- **Monolithic agent context** — leads to drift and confusion
- **Tools that prescribe process** — limits composability
- **Silent failures** — agents must stop and notify, never proceed blind
- **Port 0 in configs** — causes false-positive status detection (learned the hard way, twice)
