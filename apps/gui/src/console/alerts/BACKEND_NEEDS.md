# Alerts (A1): backend needs

Frontend only; everything below is mocked today. Shared event/state needs live in `../events/BACKEND_NEEDS.md`.

1. **Server-backed item state.** Alerts key dismiss / snooze / read / settle by EVENT id through the Console
   `StateStore` seam (`MemoryStateStore` now). Needs: load all, apply op, push channel (so a dismiss on one
   device clears the toast and badge on another). Thread-level state (`threadId`) is honoured as a fallback.
2. **Per-user alert preferences.** `AlertPrefsStore` (quiet mode, toast threshold) is localStorage-backed and
   per device. If prefs should follow the user, add a server-backed store with the same three methods.
3. **Alert-worthy events.** The mapper (`mapping.ts`) is a placeholder over F1's placeholder kinds. Backend should
   state which kinds are alerts, their severity, and a stable `subjects[]` with a navigable `url` or an offered
   `navigate` action carrying `params.href` (same-origin path or http(s) only; anything else is not rendered).
4. **Event ids and idempotency.** A re-delivered event must keep its id, otherwise it re-toasts. A state change
   that should re-alert needs a new id.
5. **Rules (A3).** Server-evaluated alert rules, if wanted, should emit pre-classified events (severity, kind) so
   this client-side severity filter can stay a pure display threshold.
6. **Snooze wake-up.** Snooze expiry is evaluated client side against wall time; a server may want to re-emit.

7. **(A3) Prefs and rules moved.** `AlertPrefsStore` is now the ConsolePrefs seam (`../prefs`), and rules live in `../rules`; see `../rules/BACKEND_NEEDS.md`.
