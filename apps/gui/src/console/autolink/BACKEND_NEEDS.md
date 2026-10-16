# Autolink renderer: backend needs (R1)

R1 is frontend only and read-only. Everything below is what the renderer would
use once it exists; until then the chip links and the hovercard says "No
preview available".

## What is live today

- `torque.task`: `createLiveResolvers(api)` wraps `/planner/torque-titles`
  (title, status, priority, updated_at). No new backend work.
- Every other kind: unresolved. The chip still links where a page exists
  (tasks, epics, projects, sprints, plans, agents tab, workstreams list); kinds
  with no resolver navigate on click instead of opening an empty drawer.

## Routes confirmed in R1 (fixed in `refs/kinds`)

| Kind | Route | Gap |
|---|---|---|
| `agent` | `/planner/agents` | The fleet page is a path tab; it ignores `?agent=`. Backend/GUI: a deep link that selects or opens one agent. |
| `workstream` | `/planner/workstreams` | `/planner/workstreams/:id` opens a `WS-` item id only. A tag name (`ws-agent-console`) needs a lookup to its item id, or a route that takes the tag. |

## Resolver endpoints still needed (batch-shaped, `ids -> {id: brief | null}`)

See `../refs/BACKEND_NEEDS.md` for the full per-kind table. For the cards R1
renders, the useful fields per kind are:

| Kind | Fields the card uses |
|---|---|
| `torque.epic/project/sprint/plan` | title, status, updated_at, plus 1-2 counts (open/done tasks) as `fields` |
| `torque.task` (richer) | description (first ~300 chars), owner/agent, epic id, sprint |
| `agent` | state, provider, cwd, last activity, current task id |
| `workstream` | name, status, open task count, owner |
| `tesseract.item/key` | title/summary, domain, namespace, key, updated_at; a GUI-reachable read path |
| `url` | server-side unfurl (title, favicon); SSRF and CORS rules apply |

Not-found vs permission-denied: `ResolveOutcome` only has `found | not-found`.
The card words not-found as "may have been deleted, or you may not have access".
A distinct `forbidden` outcome needs a contract change in `refs/types.ts` plus a
403-vs-404 distinction from the endpoints.

## Detail drawer

The drawer renders the same entity as the hovercard. Richer per-kind detail
(task checklist, recent comments, linked PRs) is registered with
`registerRefDetail(kind, Component)` and fetches its own data. Those need
read endpoints per kind; none are assumed.

## Quick actions

Phase 1 actions are open, copy id/path. Assign, comment, approve or transition
from a card need write endpoints and must go through the console ActionBus with
IdentityConfirm (D2), not through `RefAction`.

## R2 rollout notes

- Surfaces adopted (`LinkText surface=...`): task description/comments (task dialog, CommentList), planner comments and decision summaries, Agents modal (doing, blocked-on, changes seen), alert/toast detail, messaging bodies (page and RecentThread), Forums post markdown (`linkifiedMarkdownComponents`).
- Only `torque.task` resolves live; every other kind still needs a batch resolver (see above). Agent chips cannot deep-link to one agent until the fleet page reads `?agent=`.
- Not linkified on purpose: whole-row buttons (fleet row, messaging conversation list, board cards), because nested interactive elements are invalid. Needs a design call if row-level previews are wanted.
- Opt-out: `setAutolinkSurface(surface, false)` or `?autolink=off`. A server-side per-user preference would need a settings endpoint.
