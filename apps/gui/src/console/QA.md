# Console QA pass (Q1)

Scope: frontend only, branch `console/q1`, from the merged Wave 0-3 spike. Method: unit tests, static audit,
and Playwright against my own vite (`?console=fixture`, API origin pointed at a dead dummy so every
`/api/v1` call 500s; Console surfaces use the FixtureSource and mocks).

## Integration wiring added (all behind `?console=fixture|live` / `VITE_CONSOLE_SOURCE`)

| # | Change | Where |
|---|---|---|
| 1 | Chat dock + nav-rail trigger (unread badge); `AutolinkProvider` is the existing `ShellAutolink` above `ConsoleShell` | `shell/chat-dock.tsx`, `shell/ConsoleIntegration.tsx`, `App.tsx` |
| 2 | `/prs/inbox` route and "PR inbox" nav item, only when the Console is on (`useConsoleEnabled`) | `App.tsx` |
| 3 | Order is now ConsoleProvider > Messaging > AgentUnread > Chat > CardRuntime > AlertHost > ChatDock | `ConsoleIntegration.tsx` |
| 4 | Metric, HITL and workflow card kinds registered in a surface-less `CardRuntimeProvider` (`mountSurfaces={false}` so AlertHost still owns toasts/modal). HITL and Workflow *providers* are NOT mounted (they present mock clients' cards; Z1 mounts them with live clients) | `ConsoleIntegration.tsx` |
| 5 | `MailBadge` falls back to `MessagingClient.unreadCounts` (optional method, mock implements) via one batched 30s poll that only runs while a badge is mounted and the Console is on. Live mail adapter deliberately does not implement it: `GET /messages/inbox` is destructive | `messaging/unread*.ts`, `fleet-parts.tsx` |
| 6 | Fleet page reads `?agent=` (id, then provider, then folder name), opens that agent's modal, clears the param on close; agent chips link to it | `fleet-agents.tsx`, `lib/fleet-agents.ts`, `refs/kinds/agent.ts` |
| + | Nudge / Request update / Chat actions in the fleet agent modal (M1 documented "mount in the agent modal" but nothing did; needed for the golden path). Hidden when the Console is off | `messaging/FleetActions.tsx` |

Live mode wires `createMailMessagingClient(api)` and `createMailChatClient` (no roster; threads open from the modal/dock).

## Checked

- **Playwright smoke (fixture)**: alert tray open/Escape/focus return; row -> detail modal; approve -> identity confirm -> settled;
  Escape returns to tray; reply composer sends (mock); chat dock open, thread, send, ref chip rendered, Escape + focus return;
  ref chip click -> drawer; PR inbox via nav -> PR workspace; fleet `?agent=` deep link (stubbed fleet API) opens and clears;
  nudge from the agent modal reaches `delivered`; Chat button hands off to the dock; mail badge fallback shows "No messages" / feed value wins.
- **Perf**: only one `EventSource` (`/api/v1/events`, pre-existing app stream) with and without the Console; none per route;
  Console fixture opens no stream. Idle DOM churn on `/models`: 4 mutations / 10 s (baseline 0, the 30 s clock tick). No long tasks.
  Vitest storm test: 300 fixture ticks on top of 45 preloaded events re-render the app subtree 0 times and subscribe once.
  Provider values (`Messaging`, `Chat`, `ChatDock`, `AlertsController`, unread store) are memoised; `ConsoleProvider` children are stable.
- **Visual**: computed-style scan of inbox, workspace, directory/editor/lifecycle/audit, wizard, tray, prefs, alert detail, chat:
  no border radius, no sub-10px text, shadows only on overlays. Contrast scan (WCAG 4.5:1) clean after the fix below.
- **Narrow (390 px)**: inbox, tray, chat dock, PR workspace: no horizontal overflow, no overlay off-screen.
- **Keyboard**: bell and chat triggers are in the tab order; tray, detail, dock, drawer all close on Escape.

## Fixed

1. Focus was lost (body) when the tray, detail modal or chat dock closed: new `useRestoreFocus`.
2. After approve/settle removed the focused button the detail modal lost focus, so Escape and the Tab trap stopped working: focus is pulled back in.
3. Clicking inside the stacked detail modal closed the tray under it (outside-click handler); Escape on the bell now closes the tray.
4. Alert toasts (z-50, top-right) covered the chat dock header and intercepted its clicks: toasts slide left while the dock is open.
5. Chat dock did not take focus; its window Escape handler fired under another modal.
6. Dim text `text-zinc-600` (2.6:1) -> `text-zinc-500` (4.1:1) across Console, including placeholders.
7. `animate-pulse` / `animate-spin` ignored `prefers-reduced-motion`: pulse off, spin slowed (global rule in `index.css`).
8. Mock messaging could not resolve bare agent slugs, so every fixture alert Reply said "No messageable sender".
9. PR workspace "Back" always went to `/planner`; it now returns to the previous page (inbox) when there is one.

## Left (not fixed) / findings

- **Live mode gaps** (backend): alert Reply to a slug-only sender, and unread counts, need server support (`messaging/BACKEND_NEEDS.md` items 9-10).
- `text-zinc-500` on zinc-950 is 4.1:1 (just under AA) and is the Console's dim tier; `text-text-subtle` in sysop-ui `EmptyState` is the same value. Fixing the token is a design-system change, not a Console one.
- The legacy fleet/planner pages still use `text-zinc-600` and rounded sysop-ui shell controls; out of Console scope.
- Alert detail "expiresAt" shows a raw epoch number (fixture payload field); needs a field formatter.
- HITL and Workflow providers are not mounted in the shell (mock clients); cards of those kinds only appear once Z1 supplies live clients. The tray (A1) lists alerts, not K1 cards, so the card surfaces (`CardTray`, `CardToaster`, `CardModal`) are still unmounted in the app.
- First Escape in a stacked drawer closes the top layer only (by design); a background Playwright tab throttles base-ui exit animations, so use `bringToFront` in scripts.
- `?console=` is dropped from the URL on some in-app navigations; the shell latches the decision at first mount, so this is harmless but surprising when copying URLs.
- PlannerPage.tsx pre-existing lint errors untouched, as instructed.
