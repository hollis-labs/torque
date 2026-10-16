# Alert rules and Console prefs (A3): backend needs

Frontend only; everything is mocked. Shared event needs: `../events/BACKEND_NEEDS.md`; alert state: `../alerts/BACKEND_NEEDS.md`.

## Rules
1. **Event kinds are placeholders.** `rules/default-pack.ts` matches `agent.state`, `task.state_changed`,
   `message.received`, `approval.*`, `pr.*`, `budget.threshold`, `checkpoint.pending|requested|state_changed`,
   `fleet.node.*`, `alert.raised`. When the C2 catalog lands, edit the pack (data), not code. Rules can already
   match `class.category`, `class.labels`, `class.severity` (via event severity), `source.id`, subject types and payload paths.
2. **Budget and checkpoint events** do not exist yet. Needed: `budget.threshold` with `payload.percent` (0-100+) and
   `payload.scope`; a checkpoint-pending event carrying a navigable subject.
3. **Rule packs from the server.** `AlertRulePack` (version 1, JSON) is the wire shape; `parseRulePack` validates untrusted
   packs and skips bad rules with reasons. Needed: `GET` the effective pack for the user (default + agent/automation
   contributions + per-user disables), with rule ids stable so a user can disable one (`enabled:false`).
4. **Escalation is display only** (severity shown higher after N unread seconds, no re-toast). A server-side
   escalation (re-notify, page) would emit a new `alert.raised`.
5. **Coalescing is client-side** over the visible feed; counts reset if events age out of the feed window. If the
   server coalesces, it should emit one event with a `count` and a stable id per key.

## ConsolePrefs (user preferences)
6. **Server-backed prefs.** `ConsolePrefsStore` (get/set/subscribe, optional async `load`) is a localStorage mock under
   the legacy key `console.alerts.prefs.v1`. Needed: per-user storage readable across devices, plus push so a change
   on one device updates others. Fields: `quiet`, `toastMinSeverity`, `quietHoursEnabled/Start/End` (local time; consider
   storing an IANA tz so server-side delivery can honour it), `mutedSources[]`, `coalesceWindowSec`.
7. **Admin-manifest endpoints.** `consolePrefsSettingsGroup()` is the `settings` group the app should publish
   (scope `{kind:'user', id}`, group id `console.alerts`). Proposed endpoints: `GET /admin/settings/console.alerts`,
   `POST .../validate`, `.../update` (`{set, unset}`), `.../reset`. Backend validation is mandatory (kit-settings contract).
   `mutedSources` travels as one comma-separated string because the v1 profile is flat scalars only.
8. **Quiet semantics (owner decision):** critical alerts always toast, even in quiet mode, quiet hours or a muted source.
   A server that suppresses push/notification delivery must apply the same rule.

## kit-settings readiness (`@hollis-labs/kit-settings` 0.2.0)
- Published on npm (0.2.0) and a good fit (flat scalars, enum selects, booleans, integer min/max, pattern), but **apps/gui does not
  depend on it** and not on design-kit at all (only `@hollis-labs/sysop-ui`). Not vendored, not in the lockfile.
- Installing needs NEW dependencies: `@hollis-labs/kit-settings`, `@hollis-labs/design-components`,
  `@hollis-labs/design-tokens` (^0.4.0), and its CSS imports (`design-tokens.css`, `design-components/source.css`,
  `kit-settings/source.css`) added to the GUI's Tailwind entry. Peers `react` 19, `tailwindcss` 4, `@base-ui/react`
  1.3 are already present; `lucide-react` is present. It also brings its own token/appearance layer, which may clash with the
  zinc/amber Operations look; needs a visual check. Hence not forced.
- It has no persistence or transport: the host owns snapshots, drafts, validate/save/reset, ETag/If-Match, 409/412
  reconcile, and must not show optimistic "saved". Our `ConsolePrefsStore` + endpoints (items 6-7) are that host layer.

### Swap-in step (when the deps are approved)
1. `npm i @hollis-labs/kit-settings @hollis-labs/design-components @hollis-labs/design-tokens`; add the three CSS imports.
2. Replace `<ConsolePrefsPanel prefs onChange/>` (mounted in `alerts/AlertTray.tsx`) with `<SettingsRenderer
   contractVersion={1} groups={[consolePrefsSettingsGroup(userId)]} states={{[CONSOLE_PREFS_GROUP_ID]: {values:
   prefsToSnapshotValues(prefs), draft}}} onDraftChange={setDraft} onSave={(_, ch) => store.set(changesToPrefsPatch(ch,
   DEFAULT_CONSOLE_PREFS))} />`. Keep `draft` in component state; with the mock store, saving is synchronous so the new
   snapshot arrives immediately and the draft clears.
3. Delete `ConsolePrefsPanel.tsx`. `manifest.ts` (group, snapshot and changes projections) and its tests stay.
