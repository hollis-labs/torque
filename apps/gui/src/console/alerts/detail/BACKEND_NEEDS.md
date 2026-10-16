# Alert detail + actions (A2): backend needs

Frontend only; every item is mocked today. Shared state needs also in `../BACKEND_NEEDS.md` and `../../events/BACKEND_NEEDS.md`.

1. **Decision endpoint (approve / deny).** `AlertDecisionClient.decide({ eventId, decision, note?, confirmationToken?, subject? })`
   is registered on the ActionBus as `alert.approve` / `alert.deny` (`registerAlertDecisionActions`). Needs a server route that
   verifies the IdentityConfirm `confirmationToken` against the requested scope (`approval.approve`, or the scope the event offered),
   applies the decision to the approval (go-hitl is the wire), and returns `{ ok, message }`. Must be idempotent per `eventId`
   (a second decide on a resolved approval returns the first outcome, not an error).
2. **Settle after decision is two writes.** The GUI decides, then writes `settle` (resolution `approved` / `rejected`, note) to the
   StateStore. The server should make the decision write the settle itself so a failure between them cannot leave an approved
   item open on other devices. A resolved-by-someone-else event (`approval.resolved`) should settle it for everyone.
3. **Defer to a context.** Defer-to-time is a StateStore `snooze`. Defer-to-context ("when the agent is idle", "after the linked
   task closes", "next working session") is stored as a snooze with note `defer:<label>` and a 24h fallback expiry. Needs a
   first-class `defer { key, context: { kind, ref? }, until? }` op plus a server-side evaluator that reopens the item when the
   condition holds and emits a new event so every device re-alerts.
4. **Snooze resurfacing.** Expiry is evaluated client side on a 30s tick (A1). A snoozed item that wakes does not re-toast
   (the toast queue remembers ids). Server re-emit on wake would carry a new event id.
5. **Notes.** `dismiss` / `settle` / `snooze` carry an optional `note`; `StateStore` already has the field. The server should keep
   `by` (who) and expose notes to agents so a settle note can answer the originating agent.
6. **Reply target.** The reply recipient is `payload.fromAddress ?? payload.from ?? agent subject id ?? source`. Needs the producer to
   stamp a messaging URN (`payload.fromAddress`, `msg://agent/...`) on message and approval events; the live
   `MessagingClient.resolveRecipient` must accept agent slugs (the mock only knows URNs and three fixtures).
7. **Per-kind action declaration.** `defaultAlertActions` hard-codes the matrix for `approval.requested`, `message.received` and the
   rest. Producers should list the actions they support in `event.actions` (`approve`, `reject`, `reply`, ...) with `requiresConfirm`
   and `scope`; the resolver already refines scope / step-up from those offers and can start filtering the set from them.
8. **Related timeline.** Built from events already in the client by `threadId` / `correlationId`. A server query
   (`events?correlationId=`) is needed for history older than the client window.
