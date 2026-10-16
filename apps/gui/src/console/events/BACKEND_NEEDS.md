# Console backend needs (append-only; feeds task G1 CW-20261016-0110)

## F1: events, state, actions, identity

1. **Unified event feed.** `GET /api/v1/console/events?since=<cursor>&limit=` returns
   `{events: ConsoleEvent[], cursor}` and SSE `GET /api/v1/console/stream` (named events or default
   message; `Last-Event-ID` resume). Envelope as in `CONTRACT.md`. Needs: stable globally unique `id`,
   a monotonic opaque `cursor`, a `gap` event when the buffer overflowed, `source.machine` for
   multi-machine feeds. Today only activity (`/activity/stream`, `/activity/agents`) and Torque `/events` exist;
   the GUI adapts both (`ActivityStreamSource`, `TorqueEventsSource`). Torque `/events` has no ids or cursor.
2. **Kinds the data side should define** (GUI uses placeholders): `agent.state`, `task.state_changed`,
   `message.received`, `approval.requested|resolved`, `pr.opened|review_requested|checks_failed|merged`,
   `metric.tick`. Offered `actions[]` should set `requiresConfirm` and `scope` for anything authorising a change.
3. **Console item state (StateStore).** Per user: `GET /console/state` (all), `PUT /console/state/{key}` with ops
   `read|dismiss|snooze{until}|settle{resolution}|reopen`, plus push of changes (SSE or piggyback on the stream)
   so devices stay in sync. Readable by agents. `key` is an event id or thread id.
4. **Action execution.** One endpoint per action kind or `POST /console/actions {kind, eventId, params,
   confirmationToken}`; server must verify the step-up token and scope, and emit the resulting
   `approval.resolved`-style event.
5. **IdentityConfirm.** Needs the auth workstream: a step-up endpoint taking `{who, what{action,title,subject}, scope[], ttl}`
   and returning `{outcome, expiresAt, token}`; token is short-lived, bound to action+scope, verified server-side on execute.
   GUI mock shape is `ConfirmRequest`/`ConfirmResult` in `console/seams/identity-confirm.ts`.
6. **Known gap:** Fleet/activity resync replaced the list; Console store merges (never removes except size cap),
   so removal semantics (e.g. `process.exited`) must be expressed as events, not snapshot absence.

## F1 addendum: envelope v1.1 (tracks DRAFT event_message_stream_contract_2026_10_16)

7. **Log-assigned `cursor`, `epoch`, `recorded_at`** on every delivered event, and `epoch` bumped on any
   sequence restart (the activity-monitor sequence resets today without one). Snapshot responses
   should return `{events, cursor, epoch}`; the GUI sends `since=<cursor>` and drops it on epoch change.
8. **Gap frames with detail**: `{reason, earliest_available, high_water}` (Chimera sends an empty frame).
   The GUI replays from scratch when detail is present.
9. **Stamped blocks** `scope`, `corr`, `actor`, `class` from verified context, never from payloads.
   Consumers rely on `scope.task_id`/`project_id`/`worker_id` and `class.visibility` (the GUI must not
   show `operator`-only events to non-operators; enforcement is server-side).
10. **Message lifecycle events** must carry `body_ref` and never the body; need a mailbox endpoint
    resolving `body_ref` to content (authorised, sanitized) and `thread_id`/`in_reply_to` for threading.
11. **Alerts**: `alert.raised`/`alert.cleared` pair on a stable `coalesce_key` (UI collapses by it),
    with `source_event_ids` for drill-down and `confidence` tier.
12. **Approvals**: `approval.requested` must include `request_id`, `expires_at`, `hitl_ref`, and the
    offered actions with `requiresConfirm`/`scope`; `approval.resolved` the outcome and resolver.
13. **Progress**: `progress.checkpoint` with `detail_level`; `data.emitted` with `schema` (`id@version`)
    plus a schema registry so the GUI can pick renderers.
14. **`session_id` optional**: GUI keys on `subject`/`scope`, so producers should always send a `subject`
    when there is no session.
