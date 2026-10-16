# HITL cards: backend needs (H1)

Frontend only. `MockHitlClient` (client.ts) is the only `HitlClient`; the live one wraps Tangent's queue
behind the same interface. Event kinds (`hitl.<type>.requested`, `hitl.resolved`) are placeholders in
`model.ts` (`HITL_EVENT_KINDS`, `HITL_CARD_RULES`); remap to `approval.requested/resolved` etc. when the
stream design lands. Gaps vs Tangent are marked GAP.

## Mapping to Tangent interaction kinds

| Card kind | Tangent kind (tangent_interaction_kind_list) | Notes |
|---|---|---|
| `hitl.approval` | `tangent.hitl-item` | approve/reject (+defer). Outcome is terminal per item in Tangent; defer is mocked as "stays pending". |
| `hitl.question` | `tangent.interview-question` (choice/text), `tangent.feedback` (multi) | quick-picks map to `options`; free text maps to the long-form answer. |
| `hitl.form` | `tangent.form-collect` | `HitlField` is a small subset (text, textarea, number, select, checkbox). Need the real `response_schema` to map fields and validation. |
| `hitl.secret` | none | GAP: no secret interaction kind. Needs one whose response is never persisted or journaled by Tangent. |

## Needs

1. **HitlClient over the Tangent queue.** `list/get` (tangent_hitl_get, interaction_get), `respond`
   (tangent_interaction_submit), `subscribe` (queue SSE; today Tangent SSE carries no content, so push should
   carry the changed item or the client re-fetches). Items need: requester id/label/kind, risk, `expires_at`,
   context refs (type,id,label,url), status, and an audit list. Risk has no field in Tangent today.
2. **Identity step-up on approve.** The client forwards `ConfirmResult.token`; the server must verify it
   (who, scope `hitl:approve` + the request's scopes, expiry) before recording an approval, and be
   idempotent on `(request id, decision)`. The mock enforces "approve requires a granted confirmation".
3. **Secrets.** `submitSecret(id, value, ctx)` must deliver the value to its destination (e.g. Cerberus vault)
   without storing it in Tangent, the journal, the event bus or logs; the sanitize stage must treat the
   response as `sensitivity=secret` (payload never stored). The GUI keeps the value in component state until
   submit, hands it over via a one-shot `SecretHandoff` (only an opaque handle crosses the ActionBus), clears the
   input immediately and shows no reveal toggle. Transport must be TLS to the same origin.
4. **Expiry enforcement (GAP).** Tangent does not enforce `expires_at` (investigation tangent-hitl-docs-e2e).
   The GUI disables input past `expiresAt` and the mock refuses expired submits, but the server must refuse them
   and emit `approval.resolved{outcome: expired}` so every device settles the card.
5. **Decision audit.** Immutable per-request entries `{at, actor, action, note?}` including the step-up
   (`confirmed`, scope list, never the token). The card renders `audit` as is.
6. **Resolution events.** On any decision (any device, agent withdraw, expiry) emit a notification event with
   the request id so rule-derived cards close: `hitl.resolved` payload `{id}` today (`HITL_RESOLVED_KIND`).
   go-hitl stays the decision wire; the bus only notifies.
7. **Requester + withdrawal.** `withdrawn` status when the requesting agent cancels (tangent_hitl_withdraw).
8. **Operator visibility.** Approval/secret detail should be `visibility=operator` (never to agent tokens).
9. **Action params.** Answers and form values pass through ActionBus params (and so its audit listeners);
   if a form can carry sensitive data the server should mark the field `secret` and the GUI should route it via
   `SecretHandoff` the same way (not built; no field-level sensitivity exists in the schema yet).

## Reconciliation with prior art (2026-10-16, H2 pass)

Read: `hitl_approvals_gohitl_wire_approver_agent_2026_10_15` (project/portfolio-architect/memory/decisions) and
`tangent-hitl-docs-e2e-2026-10-16` (project/tangent/knowledge/investigations). H1 is consistent on the headline points
(go-hitl is the one wire, Tangent is the human queue, expiry unenforced, the requester is not the decider). Mismatches and gaps, none fixed here:

1. **Four classes, three card types.** The decision names permission asks (go-permission/Cedar), action approvals (Cerberus), questions and
   secrets/forms as one wire. `hitl.approval` covers the first two with no field telling them apart. Needs `class` (e.g. `permission`, `cerberus.deploy`,
   `secret`, `agent-definition`, `protected-push`, `spend`, `destructive-data`) on `HitlRequest` so the UI and the step-up policy can vary by class.
2. **Plan-hash binding.** Cerberus approvals bind to a plan hash and TTL. `HitlApprovalRequest` has `effects` but no `planHash`; the approve action should carry
   the hash and the client must refuse if it changed between display and submit. Add `planHash` to the type and to `respond` params.
3. **No self-approval.** The decision says the requester is never the approver (Cerberus enforces it). H1 has `requester` but no check; the
   server must enforce it and the card should hide Approve when `requester.id === confirmation.who.id` (needs the signed-in id on the card, a K1/auth item).
4. **Approver agent / assurance.** The approver agent is a go-hitl responder of kind `agent` with a lower assurance level, in shadow mode first.
   H1's audit `actor` is a free string; it needs `actorKind` (`human|agent`) and the assurance level, and the UI should label shadow-mode agent
   verdicts as advisory (not as a decision) while the owner still approves. High-risk classes need passkey step-up (human assurance); today's
   `ConfirmResult` has no assurance level, so IdentityConfirm needs `assurance` in its result when the auth API is defined.
5. **Escalate / fail closed.** The approver can approve, deny or escalate; unanswered requests fail closed. H1 has approve/reject/defer and `expired`;
   `defer` is mocked and has no go-hitl outcome. Map defer to escalate or drop it. `HitlStatus` needs `escalated`.
6. **Outcome/cause vocabulary.** The investigation says go-hitl states/outcomes/cancel causes match Tangent, `terminal_conflict` exists only in go-hitl
   (not Tangent's error schema), and expiry should be an atomic late-refusal plus a read-time computed `expired` state. H1's `HitlError` has no
   `terminal_conflict`; add it so a late/duplicate decision surfaces as "already decided" rather than a generic error.
7. **Delivery.** The requester blocks with `hitl_await`, the approver agent is notified via Tether mail; neither is a GUI concern, but `HitlClient.subscribe`
   should be fed by the same queue (no second channel, no new approval message kind), which matches the plan in Need 1.
8. **Idempotency.** Tangent enqueue is idempotent on `idempotency_key` and the docs path swallowed a conflict (CW-20261016-0075). Not GUI-visible; the
   client's decision calls should carry a client-generated idempotency key per (request id, decision), already assumed in Need 2.

### Secrets: shared secrets lib

Secrets are now resolved through the new shared secrets lib (landed 2026-10-16). The GUI must only ever hold opaque handles: H1's `SecretHandoff`
already models this (the typed value lives in component state until the single hand-off; only the handle crosses the ActionBus; no reveal toggle).
The exact lib API (handle format, submit/resolve calls, lifetime, scoping per request) was not available to H1 or H2 and **has to be reconciled when
the backend agent adopts the lib**: expected changes are confined to `HitlClient.submitSecret` (the live implementation), the `SecretHandoff` handle
shape if the lib issues its own handles, and Need 3 above. The GUI should never receive a secret value back, only status and a handle/label.
