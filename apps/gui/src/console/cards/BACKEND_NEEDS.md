# Card pipeline: backend needs (K1)

Frontend only. Everything below is mocked or derived client-side today.

1. **Server-evaluated cards (optional, later).** Rules run in the browser over the ConsoleStore event list
   (`deriveCards`). If dedupe/supersede must be consistent across devices or feed agents, the server could emit
   `CardInstance`-shaped events (kind `card.upsert`, same fields as `types.ts`); the GUI would then skip the
   rule engine for those. Rule data (`CardRuleSet`) is plain JSON so it can be served from config.
2. **Action handlers.** `approve`, `reject`, `reply`, `navigate`, `api` are mock-registered
   (`registerMockCardHandlers`). Real handlers need endpoints that accept the `ConfirmResult.token` from
   IdentityConfirm and return `{status, message}`. `approve`/`reject` must be idempotent on `(card key, actionId)`.
3. **Resolution events.** A card closes on a resolver event (`approval.resolved`, `pr.merged` by default).
   The backend must emit one when an approval is decided elsewhere, with the same `correlationId`
   (approval) or PR subject, so every device sees the card settle.
4. **Dedupe keys.** Rules key cards on `correlationId` / `threadId` / subject id. Producers should set
   `correlationId` on `approval.requested` and keep `threadId` stable per conversation.
5. **Tangent interaction kinds.** `CardKindDef` + `CardActionSpec` mirror interaction kinds loosely (kind id,
   declared actions, scopes). Need the exact `tangent_interaction_kind_list` schema to map kinds to registry entries.
6. **StateStore keys.** Card state (dismiss/snooze/settle) is stored under the card id (the dedupe key, e.g.
   `approval:appr-1`), not the event id. Server rows must accept arbitrary string keys.
