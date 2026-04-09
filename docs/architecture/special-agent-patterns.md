# Special Agent Patterns

## Status: Design — In Progress

## Overview

Special Agents are purpose-built agents with narrow scope, specific tools, and pre-loaded context. Unlike general agents that boot a profile and claim tasks, Special Agents are instantiated for a specific function and know exactly what they're looking for.

## Two-Stage Dispatch Pattern

The core pattern for reactive Special Agents:

### Stage 1: Observer Agent
- Watches a stream (task stream, chat stream, project stream)
- Has a narrow lens: only looking for one type of signal
- Cheap to run (small context, fast model like Haiku/Sonnet)
- Does NOT act on what it finds — produces a structured **pointer**
- Pointer: "messages 47-63 in stream X contain [signal type]"

### Stage 2: Specialist Agent
- Receives the pointer from the observer
- Pulls focused context: stream messages, Vanta Conduit records, files
- Produces the artifact: task, ADR, doc update, backlog item, etc.
- Born with deep knowledge of exactly one thing
- Dies after producing the artifact

### Why Two Stages?
- Observer stays cheap (no bloated context)
- Specialist gets high-accuracy input (pre-filtered, pre-located)
- Neither drifts — neither holds full conversation context
- Scales: add more observers for more signal types without touching existing ones

## Stream Hints

Participants can optionally include hints in messages to improve observer accuracy:

- `:capture_adr` — architectural decision detected
- `:capture_task` — potential task identified
- `:capture_convention` — convention or pattern worth documenting
- `:capture_decision` — design decision made

Hints are zero-cost for participants who don't care (humans/agents ignore them). High-signal for observers. Start with explicit hints, evolve to implicit detection as patterns emerge.

## Planned Special Agents

### Design Capture Agent
- **Observer scope:** ADRs, architecture docs, design philosophy, convention-setting
- **Trigger:** `:capture_adr`, or implicit detection of "the decision is", "let's go with", "I think we should"
- **Specialist:** Writes/updates ADR, architecture doc, Vanta Conduit record
- **First to build** — would have caught this entire session's decisions automatically

### Task Capture Agent
- **Observer scope:** Task candidates in conversation
- **Trigger:** `:capture_task`, or phrases like "we need to", "let's add a ticket for"
- **Specialist (TaskMan Bob):** Receives pointer, pulls chat + Vanta Conduit + files, writes detailed task with description, acceptance criteria, tags, risk level

### Backlog Capture Agent
- **Observer scope:** Ideas, future work, "nice to have" items
- **Trigger:** `:capture_backlog`, or "we should eventually", "future consideration"
- **Specialist:** Creates backlog item with proper context

### Convention Enforcer
- **Observer scope:** Code changes, PR diffs
- **Trigger:** New code committed
- **Specialist:** Checks against documented conventions, flags violations

## App Internal System Agents

Each app has a system agent as the single point of contact:

| App | System Agent Role |
|-----|------------------|
| Cerberus | Service management SME. Validates incoming commands, analyzes intent, suggests alternatives. |
| Volon | Task/sprint/orchestration SME. Knows the full lifecycle. |
| Vanta Conduit | Context/memory SME. Knows namespaces, search, assembly. |
| Mentat | Meta-agent. Planning, coordination, knowledge synthesis. |
| Hadron | Automation SME. Blueprints, pipelines, scheduling. |
| Nanite | Notes/documentation SME. |

These are NOT separate processes — they're the app's built-in intelligence layer. Chat with Cerberus = chat with the Cerberus system agent. Other agents may be involved internally, but the caller sees one interface.

## Invocation

Special Agents can be triggered:
1. **Automatically** — Observer detects signal in stream
2. **Explicitly** — User or agent invokes via MCP/CLI/GUI (`volon special-agent invoke design-capture --pointer "stream:123/msg:47-63"`)
3. **Scheduled** — Hadron runs periodic sweeps (e.g., nightly convention audit)

## Relationship to Sub-Agents

Special Agents are NOT the same as sub-agents, but sub-agents CAN be Special Agents.

- **Sub-agent:** A child process spawned by a parent agent for parallelism. Generic.
- **Special Agent:** A purpose-built agent with specific identity, scope, tools. Can be invoked as a sub-agent or independently.

The Handler/Field Agent pattern from overnight MMA testing:
- **Handler (Special Agent):** Born with full scope. Coordinates.
- **Field Agents (Special Agents):** Born with task-specific scope. Execute.
- Handler dispatches Field Agents as sub-agents, but each Field Agent is a Special Agent with its own identity and toolset.
