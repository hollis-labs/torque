# Durable requests (CW-20261120-0177)

A request is an existing **manual Torque task**, with a `request` tag and a durable request marker.
Tether owns request/reply transport, delivery receipts and the acknowledgment
watcher. Torque owns the lifecycle and transactional events. No request task is
made dispatchable by this API. Existing task reads (HTTP and MCP) expose the
marker in `metadata.request`; the dedicated request API is authoritative.

All endpoints below use the existing `/api/v1` authentication policy.

1. Create the task with `POST /api/v1/tasks`, `manual: true`.
2. Associate its immutable transport request ID using
   `PUT /api/v1/tasks/{id}/request`:
   ```json
   {"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","mail_message_id":"request-envelope-id"}
   ```
   Optional `due_ack_at` is RFC3339; default is submission time plus 15 minutes.
   Identity retries return the existing record without resetting its deadline;
   a different association or reuse of a mail ID returns 409. Registering a
   missing task returns 404; non-manual or terminal tasks return 400.
3. Read `GET /api/v1/tasks/{id}/request`. Responses are bare objects with
   `task_id`, `requester_urn`, `recipient_urn`, `mail_message_id`, `state`,
   `submitted_at`, `due_ack_at`, `ack_at`, `alerted_at`, `closed_at`, `updated_at`.
   Times are UTC RFC3339; unset times are JSON null.
4. Transition with `POST /api/v1/tasks/{id}/request/transition`:
   ```json
   {"state":"done","summary":"DONE · task-id\nOutcome: checked reports","author":"msg://agent/local/worker"}
   ```
   States are `acknowledged`, `in_progress`, `done`, `declined`, `blocked`.
   Terminal states require a nonempty `summary`, stored as a task comment.
   `author` is audit text; callers use the normal authenticated API. `reason`
   optionally explains the event. The first work/terminal transition implicitly
   acknowledges, so a fast reply need not make multiple calls. Repeated states
   and acknowledgment after work are no-ops; closed requests cannot reopen.
   Task status becomes `doing` for acknowledgment/work, `done` for done/declined,
   and `blocked` for blocked. The request lifecycle is separate from the task FSM.

An original task comment beginning `STATUS started` also acknowledges the request
atomically with comment persistence (including queued/bulk comment paths).
Editing old comments or quoting that text elsewhere does not acknowledge.

## Watcher contract

`GET /api/v1/requests?awaiting_ack=true&due_before=<RFC3339>&limit=100`
returns `{items: [...], next_after_task_id: "..."}`. Omit `due_before` to find
all unclaimed requests, e.g. to check recipient availability. Scan subsequent
pages with `after_task_id=<next_after_task_id>` until the cursor is empty;
maximum limit is 1000. The cursor orders task IDs, independent of timestamps.

For an offline recipient immediately at submission, or an overdue running
recipient at 15 minutes, Tether posts:

```json
{"state":"unacknowledged_alert","reason":"recipient_offline"}
```

This is an idempotent alert marker, not a work state. It records `alerted_at`
and removes the row from `awaiting_ack=true` results. It does nothing if already
acknowledged, closed, or alerted, and a later acknowledgment remains possible.
Tether delivers the alert to the requester, including the owner's Basalisk
channel when the owner requested the work. Alert delivery/retry is a transport
responsibility; the durable outbox event is available for replay after failures.

## Events and consistency

`request.submitted`, `request.acknowledged`, `request.state_changed`,
`request.unacknowledged_alert`, `request.closed` use the existing transactional
outbox/SSE stream. Subject is `msg://task/torque/{id}`; scope contains `task_id`.
Payload is the full request record plus `from` and optional `reason`;
`class.payload_schema` is `urn:hollis-labs:events:request_lifecycle@1`.
Consumers deduplicate by event ID. Lifecycle, metadata marker, summary comment,
task status and events commit or roll back together. Concurrent retries produce
one acknowledgment/alert/closure. The canonical request record is in
`task_requests`; do not edit `metadata.request` directly. Future activity
snapshots can correlate by task ID without changing transport ownership.
