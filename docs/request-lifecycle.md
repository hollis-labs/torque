# Durable requests (CW-20261120-0177)

A request is an existing **manual Torque task**, with a `request` tag and a durable request marker.
Tether owns request/reply transport, delivery receipts and the acknowledgment
watcher. Torque owns the lifecycle and transactional events. No request task is
made dispatchable by this API. Existing task reads (HTTP and MCP) expose the
marker in `metadata.request`; the dedicated request API is authoritative.

All endpoints below use the existing `/api/v1` authentication policy.

1. **Before sending mail**, `POST /api/v1/requests/submit`:
   ```json
   {"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","send_key":"stable-sender-scoped-key","subject":"Check reports","body":"Check yesterday's reports","deliverable":"Report summary","recipient_online":true}
   ```
   Response is a bare request record (HTTP 200), including `task_id` and
   `send_key`; `mail_message_id` is initially an empty string. This atomically
   creates a manual task and registers its lifecycle. The `(requester_urn,
   send_key)` pair is unique. Retrying returns the current record, without
   creating a second task or resetting timestamps. Changed recipient, subject,
   body, deliverable, or explicit task link returns 409. Keys are scoped to
   requester: another requester can use the same key.

   Optional `task_id` explicitly links an **open manual task** instead of
   creating one; it must not already represent another request. Missing task
   returns 404; a closed/nonmanual task returns 400. A mail `re` or thread parent
   is not a lifecycle task link and must never be inferred as `task_id`.
   This first implementation accepts one recipient per request; recipient
   lists are rejected.

   `recipient_online` defaults to true. False makes `due_ack_at` equal submission
   time for immediate alerts. Otherwise the deadline defaults to submission
   plus 15 minutes; optional `due_ack_at` accepts RFC3339. The first submission's
   deadline and online snapshot win; retries cannot move the deadline merely
   because recipient liveness changed.
2. Put `task_id` into the transport request envelope and send with the same
   sender-scoped send key. The transport allocates its actual mail ID.
3. Attach that ID using `PUT /api/v1/tasks/{id}/request/mail`:
   ```json
   {"mail_message_id":"actual-transport-generated-id"}
   ```
   Same ID retries are no-ops; a different ID or an ID used by another request
   returns 409. Attachment also works after acknowledgment or closure. If a
   crash interrupts attachment, retry the idempotent transport send to recover
   its ID, then attach it. An unfiltered `GET /api/v1/requests` scan includes
   records whose `mail_message_id` is empty for repair after restarts.
4. Read `GET /api/v1/tasks/{id}/request`. Responses have `task_id`, `send_key`,
   `requester_urn`, `recipient_urn`, `recipient_online`, `mail_message_id`, `state`, `submitted_at`,
   `due_ack_at`, `ack_at`, `alerted_at`, `closed_at`, `updated_at`. Times are UTC
   RFC3339; unset times are JSON null. `send_key` is absent only for the legacy
   direct-link registration path.
5. Transition with `POST /api/v1/tasks/{id}/request/transition`:
   ```json
   {"state":"done","summary":"DONE · task-id\nOutcome: checked reports","author":"msg://agent/local/worker"}
   ```
   States are `acknowledged`, `in_progress`, `done`, `declined`, `blocked`.
   Terminal states require a nonempty `summary`, stored as a task comment.
   `author` is audit text; callers use the normal authenticated API. Include
   evidence in the summary. `reason` optionally explains the event. The first
   work/terminal transition implicitly acknowledges, so a fast reply need not
   make multiple calls. Repeated states and acknowledgment after work are
   no-ops; closed requests cannot reopen. Task status becomes `doing` for
   acknowledgment/work, `done` for done/declined, and `blocked` for blocked.
   The request lifecycle is separate from the task FSM.

For callers that already have both a task and an immutable actual mail ID,
`PUT /api/v1/tasks/{id}/request` still supports direct association with
`{requester_urn, recipient_urn, mail_message_id, due_ack_at?}`. This path does
not create tasks and is not the submit-before-send transport flow.

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
recipient at 15 minutes, Tether first sends idempotent alert mail to the requester.
Only after successful delivery does it mark the alert with:

```json
{"state":"unacknowledged_alert","reason":"recipient_offline"}
```

This is an idempotent alert marker, not a work state. It records `alerted_at`
and removes the row from `awaiting_ack=true` results. It does nothing if already
acknowledged, closed, or alerted, and a later acknowledgment remains possible.
Tether delivers the alert to the requester, including the owner's Basalisk
channel when the owner requested the work. Deliver-before-mark permits retries
after interrupted mail sends; use an idempotent alert mail key. Alert delivery
is a transport responsibility; the event records the committed alert marker.

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
