# Unified Messaging Architecture

## Status: Design — Approved Direction

## Overview

Every interaction in Fragments Engine is a conversation with the system. Whether a user files a task, chats with Mentat, receives a Cerberus alert, or an agent coordinates with another agent — it's all messages in streams. The context (task, sprint, project, app, session) determines routing, visibility, and behavior. The primitive is always the same.

## Core Principle

Comments, chat, A2A messages, A2U notifications, agent output, alerts, and handoffs are all variations of **scoped message streams between participants (agents, users, systems)**. One primitive, many views.

## Data Model

```
streams
  id              TEXT PRIMARY KEY
  scope           TEXT    -- task | sprint | session | project | epic | ad-hoc
  scope_id        TEXT    -- T-1234, S-045, etc.
  created_at      DATETIME
  metadata        JSON

stream_participants
  stream_id       TEXT REFERENCES streams(id)
  participant_type TEXT   -- agent | user | system
  participant_id  TEXT    -- agent:mentat, user:chrispian, system:cerberus
  role            TEXT    -- owner | member | observer
  joined_at       DATETIME

messages
  id              TEXT PRIMARY KEY
  stream_id       TEXT REFERENCES streams(id)
  from_type       TEXT    -- agent | user | system
  from_id         TEXT
  type            TEXT    -- chat | comment | alert | notification | handoff | output
  body            TEXT    -- markdown
  metadata        JSON    -- {task_id, session_id, ...}
  created_at      DATETIME
```

## Behavior

- **Every Volon object auto-gets a stream.** Task created = stream created. Sprint created = stream created.
- **Agent claims task = joins the stream.** User opens task detail = sees the stream as "comments" or "chat" depending on UI mode.
- **Agent output streams to the task stream** in real-time (type=output).
- **Cerberus fires an alert** = system message posted to the project stream (type=alert).
- **Agent coordination** = agents share a stream scoped to whatever they're coordinating on (type=chat).
- **Handoff** = special message type in a session stream (type=handoff).
- **User chats with agent during task** = same task stream, user is a participant.

## Use Cases

| Scenario | Stream Scope | Participants | Message Type |
|----------|-------------|--------------|--------------|
| Task comment | task | user, agents | comment |
| User chatting with agent during task | task | user, agent | chat |
| Agent output while working | task | agent | output |
| A2A coordination | task or ad-hoc | agent, agent | chat |
| Cerberus alert | project | system:cerberus | alert |
| Sprint-level context note | sprint | user, agents | comment |
| Agent handoff | session | agent(old), agent(new) | handoff |
| Mentat chat session | session | user, agent:mentat | chat |

## Service Ownership

### Volon (write path)
Volon owns the message stream primitive. Streams are tightly coupled to Volon objects (tasks, sprints, sessions). All writes go through Volon API/MCP.

- `POST /v1/streams/:id/messages` — post a message
- `GET /v1/streams/:id/messages` — read stream
- `POST /v1/streams` — create ad-hoc stream
- MCP: `volon_message_send`, `volon_messages_list`, `volon_stream_create`

### Vanta Conduit (read/discovery path)
Vanta Conduit indexes messages for cross-project search and context assembly. It doesn't own messages — it indexes them. Agents needing to find a message from three days ago use Vanta Conduit. ContextBroker can assemble message history as part of task context.

### Why Not Vanta Conduit as Owner
Vanta Conduit is system-agnostic. It provides context functionality, not execution coordination. Coupling "memory system" to "agentic orchestration" is a code smell. Vanta Conduit should work for anyone, even someone not using Volon. Messages are execution artifacts that become context over time — Volon creates them, Vanta Conduit remembers them.

## Three Agent Types

1. **App Internal / System Agents** — The app IS the agent. Chat with Cerberus = chat with the Cerberus system agent. One point of contact per app. These are SMEs and gatekeepers for their domain. Includes internal process agents (PipelineSupervisor, intent analyzers, etc.).

2. **Process / Special Agents** — Born for a specific job with specific context. The Handler/Field Agent pattern. Sprint agents, task agents, the Planner. Instantiated, do work, complete. Live in Volon's execution pipeline.

3. **General Agents** — Standard worker pattern. Boot a profile, claim tasks, execute. Any system can use these.

## Design Philosophy

- **Tools don't prescribe process, they provide functionality.** That's what lets us build anything with them.
- **Tight coupling is a code smell.** Each service stays in its lane. Volon = execution. Vanta Conduit = memory. Cerberus = infrastructure. Hadron = automation.
- **Streams are deeply embedded.** You are always chatting with the system. The context determines the specifics but the interaction model is always a stream.
- **A2A and A2U use the same primitive.** The only difference is participant types.

## Relationship to Existing Systems

- **Task comments** become messages in the task's stream (migration from flat comments table)
- **Mentat Chat** sessions become streams with type=chat
- **Cerberus alerts** become system messages in project streams (replaces file-based alerts)
- **Agent handoff** (BLG-20260312-024) uses the stream primitive with type=handoff
- **ContextBroker** assembles relevant stream history as part of task context assembly

## Open Questions

- Retention policy: how long do messages persist? Archive after sprint close?
- Rate limiting: how to prevent stream flooding from verbose agents?
- Presence: should we track who's "online" in a stream?
- Subscriptions: push notifications when a stream gets a new message?
- Stream forking: when a conversation branches, do we fork the stream or create a linked ad-hoc stream?
