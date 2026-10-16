# Chat: backend needs (C1)

The GUI codes to `ChatClient` (`types.ts`). `mock.ts` backs dev and tests (streaming replies from
fixtures). `mail.ts` (`createMailChatClient`) wraps M1's `MessagingClient`, so it is mail-only:
no token streaming, replies found by polling. Event kinds in `ChatEvent` are placeholders; map real
frames to them in an adapter, never in components.

## Session-stream client (live agents)

Needed for `mode: 'live'`. Expected source: Nanite chat-stream / Outpost hook sessions (draft stream
design: message.* events carry `body_ref` only; bodies come from the mailbox client).

1. **Capability per agent.** `GET /agents/{id}/chat` -> `{ mode: live|mail-only|offline, sessionId?, detail }`
   plus a push when it changes (`capability` event). Today nothing says whether an agent has a hook
   attached; the Fleet row only has `sessionId`, and the catalog-agent to session join is unresolved
   (see agents/BACKEND_NEEDS.md #3).
2. **Stream endpoint.** `GET /chat/threads/{id}/stream` (SSE; EventSource cannot send bearer headers, so
   same-origin proxy injection like Hub, or a short-lived stream token). Frames, mapped 1:1:
   - `message.start {id, thread_id, from, at}` -> `message` with `streaming: true`
   - `message.delta {id, text}` -> `delta` (append-only; need a per-message `seq` for resume/dedupe)
   - `tool.start|tool.end {message_id, tool_id, name, status, summary}` -> `tool` (summary must be redacted server-side)
   - `message.end {id, error?}` -> `end`
   - `message.delivery {id, state}` -> `delivery`
   Resume with `Last-Event-ID`. Because message.* events carry `body_ref` only, state whether delta text
   is inline or fetched, and the mailbox read to resolve a `body_ref` to final text.
3. **Send into a live session.** `POST /chat/threads/{id}/messages {body, idempotency_key}` that injects
   the turn through the Outpost hook (not just mail), returning the id and initial state. Interrupt/cancel
   of an in-flight reply (`POST .../cancel`) is not wired in the UI yet; confirm it exists.
4. **Operator identity** for attribution and routing replies back (shared with messaging #5).

## Mail path gaps

1. **Per-peer history with cursor.** `GET /messages?peer=<urn>&before=<ts>&limit=` (shared with
   messaging #1). `mail.ts` paging is approximate: `MessagingClient.recentThread` has no cursor.
2. **Unread.** `unread` per thread must be server-side (StateStore `read` ops keyed by thread id, so it
   is shared across devices) plus a push on change. `mail.ts` counts agent messages seen while
   subscribed only, and `subscribeThreads` is a no-op. Needed also so an unread conversation can raise
   an alert (see below).
3. **Thread list.** `GET /chat/threads` (agents the operator has talked to, last message, unread).
   Today the host passes `agents` to `createMailChatClient`.
4. **Delivery state** and batch status polling: as messaging #2 and #3.

## Alerts

`useChatUnread()` gives the total. For the alert tray, emit an event (placeholder kind
`chat.message.unread {thread_id, agent, count, preview}`) from the server when an agent message lands
on an unread thread, so A1's mapping can raise one alert per thread and settle it when the thread is
read. Not wired in the GUI: alerts are event-driven and this needs a real event.

## Open questions

- Does `live` imply the agent is mid-turn-safe to interrupt, or should a message to a busy live agent
  be queued and labelled so? The composer shows only capability today.
- Should an offline agent's queued messages expire?
- One thread per agent (current) or multiple topics per agent via `thread_id`?
- Message body format: plain text with Linkified refs (current) or markdown? Markdown rendering is not
  built; code fences are only skipped by the linker.
