# Console cards (K1)

`import { ... } from '@/console'`. Needs `<ConsoleProvider>` above `<CardRuntimeProvider>`.

Flow: `ConsoleEvent[]` -> `deriveCards(events, CardRuleSet)` -> `CardInstance[]` -> `CardView` via `CardRegistry` -> host surface.

- **Rules** (`rules.ts`, data only): `match` (kinds / prefix / severity / subject / payload) -> `card` (kind, surfaces, `dedupeKey`
  template, title/summary templates, ttl or `expiresAtPayload`, `supersedes`, declared `actions`). `resolvers` close cards.
  Templates: `{payload.x}`, `{subject.type}`, `{subject.type.label}`, `{id|kind|threadId|correlationId}`, `{a|b}` = first non-empty.
  To remap F1's placeholder kinds, pass a different `rules` to `CardRuntimeProvider`.
- **Registry**: `registry.register({id, label, render:{expanded, compact?, inline?}, actionSchema?, surfaces?, priority?})`.
  Unknown kinds fall back to the generic card. Reference kinds: `info`, `approval`, `status`.
- **Actions**: `CardActionSpec {id,label,intent,kind,confirm?,settlesAs?}`; `kind` is the ActionBus handler. `confirm` => IdentityConfirm
  first. `dismiss`/`snooze` go to the StateStore. Results/errors render inside the card. `settlesAs` settles the card on success.
- **Liveness**: cards are derived, so new events coalesce (count), resolvers/settle close them, ttl expires them.
- **Host surfaces** (consumed by A1/H1): `<CardTray/>`, `<CardInlineSlot subject=.../>`, `<CardToaster/>` and `<CardModal/>`
  (auto-mounted; `mountSurfaces={false}` to opt out), `useCardHost()` -> `toast(cardId)`, `openModal(cardId)`, `openTray()`,
  `present(card)` for non-event cards. `useCards({surface, kinds, subject, includeHandled, includeClosed})`, `useCard(id)`, `CardView`.
- Not mounted in `App.tsx`; showcase on `pages/WidgetPreviewPage.tsx` (`CardsPreview`). Backend needs: `BACKEND_NEEDS.md`.
