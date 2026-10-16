# Console event contract (F1)

Entry point: `import { ... } from '@/console'` (events + seams).

**Envelope** (`ConsoleEvent`, `events/types.ts`): `id` (dedupe key), `ts` (epoch ms), `kind` (open
namespaced string), `source {id, machine?}`, `subjects[] {type,id,label?,url?}`, `severity`
(debug..critical), `payload` (bag), `correlationId?`, `threadId?`, `actions[]` (offers), `extra`
(unknown top-level fields, preserved). `parseConsoleEvent` is tolerant: it only rejects input with no
usable `id`+`kind`, accepts `time`/`ts`, snake/camel ids, and drops malformed refs/actions.

**Additive rules**: never remove or retype a field; add new optional ones. New kinds need no GUI
change (consumers fall back to a generic row). `KINDS` lists kinds the GUI has typed payloads for.

**Source seam** (`ConsoleSource`): `snapshot({since?})` + `subscribe(handlers, {cursor?})` with
`onEvents/onStatus/onGap`. The store subscribes first, buffers until the snapshot lands, merges
by id (a snapshot never replaces the list), and resyncs on every `live` status, gap, and a slow timer.
Adapters: `ActivityStreamSource`, `TorqueEventsSource`, `CompositeSource`, `FixtureSource`.

**Mounting**: `<ConsoleProvider>` once at the shell (inside ApiProvider); it owns the only
subscription. Routes use `useConsoleEvents(filter)`, `useConsoleState()`, `useItemState(key)`,
`useConsoleSeams()`. Mode: `?console=fixture|live`, else `VITE_CONSOLE_SOURCE`, else live.

**Seams** (`console/seams`): `StateStore` (server-backed read/dismiss/snooze/settle; `MemoryStateStore`),
`ActionBus` (per-kind handlers; runs `IdentityConfirm` first when an action carries `confirm`),
`IdentityConfirm` (request: who/what/scope/ttl; result: outcome/expiry/token; `MockIdentityConfirm`).

## Envelope v1.1 tracking (DRAFT)

This section tracks the **draft** `event_message_stream_contract_2026_10_16` (Tesseract, workspace,
project/portfolio-architect/workspace/design). Nothing is implemented backend-side; field names are
proposals and may change. Everything below is optional and additive; v1 events parse unchanged.

- **New optional `ConsoleEvent` fields:** `sessionId`, `subject`, `scope` (dimension block), `corr`
  (traceId, spanId, causationId, correlationId, threadId, idempotencyKey, depth), `actor`
  (kind, id, onBehalfOf, verified), `class` (category, severity, sensitivity, pii, visibility,
  payloadSchema, labels, sanitized, redactions), `recordedAt`, `epoch`, `cursor`. The parser accepts
  the draft's snake_case and maps to camelCase. Top-level `severity` wins over `class.severity`;
  `corr.correlationId/threadId` back-fill `correlationId/threadId`. These keys no longer land in `extra`.
- **`session_id` is optional.** Fleet, daemon, message and task events may have no session; use
  `subjects`/`subject`/`scope` to attribute. Do not key UI on `sessionId`.
- **Kinds** (placeholders, see `KINDS`, `MESSAGE_KINDS`, `isStateChanged`, `isMessageLifecycle`):
  `<entity>.state_changed`, `gate.evaluated`, `phase.started|completed`, `progress.checkpoint`,
  `data.emitted`, `step.executed`, `tool.call.started|finished`, `message.*` lifecycle,
  `approval.requested|resolved`, `fleet.node.*`, `event.classified`, `alert.raised|cleared`.
  Payload interfaces for the new families are snake_case (the payload bag is untouched); older F1
  payload interfaces stay as they were. `message.received` (F1 placeholder) is kept, and
  `isMessageLifecycle` excludes it.
- **Messages never carry a body**: lifecycle events have `body_ref` (+ `body_bytes`, `content_type`).
  Fetch bodies from the mailbox via the messaging client.
- **Resume/gap model:** when events carry `epoch` and `cursor`, the store tracks `epoch`; an epoch
  change invalidates the cursor (cursor reset, full re-snapshot, events still merged by id). A gap
  frame (`GapInfo`: reason, earliestAvailable, highWater) is kept in `state.gap` until a snapshot
  heals it, and forces a replay from scratch (`since: null`). Gap frames with no detail (Chimera
  today) and feeds without epoch/cursor fall back to the F1 behaviour: resync from the last cursor and
  merge by id.
- **Placeholders:** all kind names and payload fields; category/visibility/sensitivity vocab; event id
  scheme (draft D6); `parent_id` vs `corr.causation_id` (D1). `FixtureSource` emits one sample of each
  family (`fixture-families.ts`, ids `fx-fam-N`) when `initial` is not passed, and every 5th `tick()`.
  `restartEpoch()` and `simulateGap(info)` exercise the resume model.
