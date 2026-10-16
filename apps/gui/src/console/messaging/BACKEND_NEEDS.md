# Messaging: backend needs (M1)

The GUI codes to `MessagingClient` (`types.ts`). The mock (`mock.ts`) backs dev and tests. The
mail adapter (`adapter.ts`, `createMailMessagingClient`) already maps onto the existing
`/api/v1/messages` routes through `ApiClient`; the items below are what is missing or unconfirmed
for it to be correct and complete.

## Routes the adapter uses today

| Client method | Route | Notes |
|---|---|---|
| `send` | `POST /api/v1/messages` | Body is `SendMessageRequest`. Intent maps to `kind`: message and nudge -> `notice`, update_request -> `request`. |
| `getDelivery` | `GET /api/v1/messages/{id}` + `GET /api/v1/messages/thread/{thread_id}` | Two calls per poll. |
| `recentThread` | `GET /api/v1/messages/thread/{thread_id}` | Only by thread id. |

## Needed

1. **Per-recipient history.** `recentThread({with})` has no route: threads are fetched by id only,
   and many envelopes have an empty `thread_id`. Need `GET /messages?peer=<urn>&limit=` (messages
   between the operator and a peer, both directions, oldest/newest ordering stated).
2. **Delivery state.** Envelopes expose only `delivered_at` and `consumed_at`. The GUI maps
   consumed -> `read`. Need an explicit `state` (queued/delivered/read/replied/failed) or at least
   `failed_at` + reason, and a server-computed `replied` (a reply with `in_reply_to` = this id).
   The adapter currently infers replied from the thread, which misses replies on other threads.
3. **Cheaper status polling.** A batch status endpoint (`GET /messages/status?ids=`) or the SSE
   message stream filtered to the operator, instead of polling per message.
4. **Recipient resolution.** `resolveRecipient` only parses the URN today. Need
   `GET /messages/recipients/resolve?ref=` (agent slug or name -> URN, kind, label, broadcast flag)
   so an agent without a registered URN is reported unresolvable server-side too, and group
   membership size is available for the broadcast confirmation text.
5. **Operator identity.** `from` defaults to `msg://user/local/operator`. Need the signed-in
   user's URN (from the auth workstream) so sends are attributed and replies route back.
6. **Server-side guard rails.** The GUI rate-limits nudges (one per agent per 2 minutes, in
   memory) and requires a confirmed broadcast. These must be enforced again server-side:
   nudge rate limit keyed by (operator, recipient), broadcast confirmation flag, body size cap
   (GUI caps 4000 characters).
7. **Metadata.** Sends carry `console.intent`, `console.template`, `console.expected_response` in
   `metadata`. Confirm the store keeps arbitrary string metadata and returns it on read; the
   recipient agent should be told about `expected_response` (render it in the inbox projection).
8. **Idempotency.** A retry after a timeout can double-send. Accept a client `Idempotency-Key`
   (the GUI can supply `console-<uuid>`).

## Open questions

- Should nudge be a distinct `kind` (e.g. `nudge`) rather than `notice` + metadata, so agents can
  prioritise it? `MessageKind` is a closed list in `lib/types.ts` today.
- Do agents without a live session still receive mail (queued), and should the composer say so?
- Does `read` mean the agent consumed it (`consumed_at`) or a human marked it read?
