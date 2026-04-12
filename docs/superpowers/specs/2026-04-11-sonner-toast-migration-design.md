# Sonner Toast Migration — Design

**Date:** 2026-04-11
**Status:** Approved, ready for implementation plan
**Scope:** Frontend — `apps/gui/`
**Next step:** `writing-plans` skill → implementation plan → subagent-driven execution → PR → Copilot review → merge

## Problem

The Clockwork GUI has ~16 mutation handlers that silently swallow errors. When a user triggers an action (transition a task, add a comment, create a sprint, delete a project) and the API call fails, nothing visible happens: the handler's `catch {}` eats the error, the optimistic UI doesn't roll back visibly, and the user is left to guess whether their action worked.

Sonner's `<Toaster />` is already mounted in `apps/gui/src/App.tsx` with a themed dark wrapper (`apps/gui/src/components/ui/sonner.tsx`) and icons for success/info/warning/error/loading. Nothing currently imports `toast` from sonner anywhere in the app — the infrastructure is in place but unused.

Goal: turn silent-catch patterns into visible error feedback, with a single-source-of-truth module that the whole frontend consumes.

## Non-goals

- **Success toasts.** Every mutation already has visible confirmation (badge change, list refresh, row update). Firing success toasts on top of that is noise for zero UX gain. `notifySuccess` is exported for future use but has zero call sites in this pass.
- **Initial-load error replacement.** The `EmptyState variant="error"` pattern (Board, Sprints, Epics, Projects, Runs, Dashboard) is by design — it carries a Retry action that a transient toast would lose.
- **Save-error banner replacement.** The red banner under `TaskDetailHeader` in `TaskDetailPage.tsx` is contextually anchored to the edit form and should stay visible while the user fixes the problem. Not a silent fail.
- **Best-effort prefetch catches.** View-mode name resolution and lazy tab loading in `TaskDetailPage.tsx` (L90/L98/L106/L132/L135/L138) are supposed to degrade silently — those remain as-is.
- **Toaster config changes.** Position, theme, icons, classNames stay.

## Architecture

One new module: `apps/gui/src/lib/toast.ts`. Call sites never import sonner directly.

```ts
// apps/gui/src/lib/toast.ts
import { toast } from 'sonner'

// Single source of truth for unwrapping thrown values into user-facing text.
// When the API begins returning structured errors ({code, message, field},
// validation details, etc.), this is the one place that changes.
function extractMessage(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) return err.message
  if (typeof err === 'string' && err.length > 0) return err
  return fallback
}

// Call from catch blocks. Keeps the try/catch visible at the site so the
// site-specific state update flow stays readable.
export function notifyError(err: unknown, fallback: string): void {
  toast.error(extractMessage(err, fallback))
}

// Exported for future use. Errors-only policy in this pass means no call
// sites yet, but this is the idiomatic import path so future work never
// reaches for sonner directly.
export function notifySuccess(message: string): void {
  toast.success(message)
}
```

**Design principles:**

1. **Module, not component.** Toasts are imperative (fired from event handlers after `await`), not declarative. `<Toaster />` in `App.tsx` is the component layer; this module is the policy layer on top.
2. **Not a hook.** Stateless helpers don't need `useX()` ceremony. If state-tracking needs emerge later (pending-mutation lists, debouncing), a `useAction()` hook can be added to the same module without disturbing existing call sites.
3. **Sealed surface.** `toast` is never re-exported. The only exports are `notifyError` and `notifySuccess`. This prevents call sites from drifting back to direct sonner use.
4. **Swappable backend.** If sonner is ever replaced, one file changes — not 16.
5. **Additive growth.** Future concerns — structured error decoding, toast.promise for pending mutations, dedup/grouping, action buttons (Retry/Undo) — land in this one module. Call sites never change in response.

This aligns with the project-wide rule: prefer abstractions over duplication; one source of truth is critical for human and agent maintainability.

## Call-site inventory

All 16 sites get the same treatment: wrap with try/catch (where missing), preserve the existing state-update logic verbatim, add `notifyError(err, fallback)` in the catch. Nothing else changes.

| # | File | Handler / Line | Fallback message |
|---|------|----------------|------------------|
| 1 | `pages/TaskDetailPage.tsx` | `handleTransition` (~L162–170) | `Failed to update task status` |
| 2 | `pages/TaskDetailPage.tsx` | `handleAddComment` (~L156–160) | `Failed to add comment` |
| 3 | `pages/BoardPage.tsx` | `handleTransition` (~L83–90) | `Failed to update task status` |
| 4 | `pages/EpicDetailPage.tsx` | catch at L46 | `Failed to update epic` (verify) |
| 5 | `pages/EpicDetailPage.tsx` | catch at L82 | `Failed to update epic` (verify) |
| 6 | `pages/SprintsPage.tsx` | catch at L113 | `Failed to create sprint` (verify) |
| 7 | `pages/SprintsPage.tsx` | catch at L122 | `Failed to delete sprint` (verify) |
| 8 | `pages/SprintDetailPage.tsx` | catch at L56 | `Failed to update sprint` (verify) |
| 9 | `pages/SprintDetailPage.tsx` | catch at L92 | `Failed to update sprint` (verify) |
| 10 | `pages/ProjectsPage.tsx` | catch at L101 | `Failed to create project` (verify) |
| 11 | `pages/ProjectsPage.tsx` | catch at L110 | `Failed to delete project` (verify) |
| 12 | `pages/ProjectDetailPage.tsx` | catch at L52 | `Failed to update project` (verify) |
| 13 | `pages/ProjectDetailPage.tsx` | catch at L109 | `Failed to update project` (verify) |
| 14 | `pages/EpicsPage.tsx` | catch at L114 | `Failed to create epic` (verify) |
| 15 | `pages/EpicsPage.tsx` | catch at L123 | `Failed to delete epic` (verify) |
| 16 | `pages/SettingsPage.tsx` | catch at L32 | `Failed to update setting` (verify) |

**Verify-on-implementation rule:** Messages marked `(verify)` are best guesses from grep context. During implementation, read each handler's full body to confirm the verb/noun match the actual API call. The fallback only fires when the thrown error has no message, so API-sourced errors still surface correctly even if the fallback guess is slightly off — but we should still get it right.

**Special case — `handleAddComment`:** The only site with *no* existing try/catch. The current code is:

```ts
async function handleAddComment(content: string) {
  if (!id) return
  const comment = await api.addComment(id, content)
  setComments((prev) => [...(prev ?? []), comment])
}
```

Wrap the entire body (after the `id` guard) in try/catch. The `setComments` call must stay *inside* the try block so it doesn't fire on failure and append a ghost comment.

## Testing strategy

### Layer 1 — Module unit tests (`apps/gui/src/lib/toast.test.ts`)

New test file. Mocks `sonner` via `vi.mock('sonner', ...)` and asserts on the calls made to `toast.error` / `toast.success`. Five cases pin the unwrap policy:

1. `notifyError(new Error('boom'), 'fallback')` → `toast.error('boom')`
2. `notifyError('explicit string', 'fallback')` → `toast.error('explicit string')`
3. `notifyError(null, 'fallback')` → `toast.error('fallback')` (also covers `undefined`, `{}`, `42`)
4. `notifyError(new Error(''), 'fallback')` → `toast.error('fallback')` (empty-message edge case)
5. `notifySuccess('all good')` → `toast.success('all good')`

Future structured-error support adds cases here without touching call sites.

### Layer 2 — Targeted call-site smoke tests

**No new test files.** Two toast assertions added to existing suites, targeting the two sites the boot prompt explicitly names:

1. `TaskDetailPage` test: force `api.transitionTask` to reject, trigger the handler, assert `notifyError` was called with the error instance and `'Failed to update task status'`.
2. `BoardPage` test: same shape for its `handleTransition`.

Pattern:

```ts
vi.mock('@/lib/toast', () => ({
  notifyError: vi.fn(),
  notifySuccess: vi.fn(),
}))
```

**Constraint:** Before adding either, check the target test file for existing `vi.mock` calls that could collide. If a collision exists, skip that site's unit test and rely on the module unit tests plus browser verification.

The remaining 14 sites are covered by the module unit tests (which pin the unwrap policy) plus the trivial shape of each call — extra tests would be diminishing-return boilerplate.

### Layer 3 — Browser verification

Per the boot prompt: force both paths in a real browser.

- Cerberus-managed frontend on `:5175` and API on `:8990`.
- Valid transition on a task → no toast (errors-only policy), badge updates.
- Invalid transition or API rejection → red toast bottom-right with the API's error message.
- Post a comment with the API returning 4xx → red toast.
- Stream cerberus logs during verification; report exactly what was observed, including anything that couldn't be reliably reproduced.

If any failure path can't be forced without destructive actions (killing services, corrupting state), say so explicitly rather than claim success.

## Risks and mitigations

1. **Wrong fallback at a call site.** The `(verify)` rows are grep-based guesses. *Mitigation:* read each handler fully during implementation. Fallbacks only fire on message-less errors, so API errors still surface correctly regardless.
2. **Hidden dependency on silent-fail behavior.** If any caller relied on a mutation failing silently (e.g., optimistic UI that needed the catch to eat the error), surfacing a toast could look like a regression. *Mitigation:* preserve the existing state-update logic verbatim at every site — only the toast is added. No state flow changes.
3. **Test mocking drift.** `vi.mock('@/lib/toast', …)` could collide with existing mocks in target suites. *Mitigation:* check each file for pre-existing mocks before editing; skip the site if conflict, rely on module + browser coverage.
4. **Bundle cost.** None. Sonner is already in the bundle via the mounted Toaster.

## Rollback

- Feature branch → PR → Copilot review → merge. If regression post-merge, revert the PR.
- Surgical rollback per site is possible: remove the `notifyError` line, restore original `catch {}`. Zero cross-site coupling.

## Scope guards (do NOT drift)

- Do not touch `EmptyState` error views.
- Do not touch the `saveError` banner in `TaskDetailPage`.
- Do not add success toasts in this pass.
- Do not migrate best-effort prefetch catches (name resolution, lazy tabs).
- Do not change `App.tsx` `<Toaster />` config.
- Do not install new packages (sonner is already present).
- Do not create a `useAction` hook yet.

## References

- `.agentrc/boot-prompt.md` — Frontend session section names this work as priority.
- `apps/gui/src/App.tsx:116` — existing Toaster mount.
- `apps/gui/src/components/ui/sonner.tsx` — themed wrapper.
- Memory: `feedback_one_source_of_truth.md` — "prefer abstractions over duplication" rule that motivates the `@/lib/toast` module shape.

## Implementation deltas (verified 2026-04-11)

The grep-derived inventory in the "Call-site inventory" table needed two corrections during implementation. The corrected scope is **13 mutation handlers**, not 16, with no change in spec intent.

### Excluded — list-fetch silent degradation (3 sites)

These three sites pattern-matched the `} catch {` grep but are NOT mutation handlers — they are list-fetch silent-degradation catches, structurally identical to the "best-effort prefetch catches" already excluded in the Non-goals section. Each falls back to `setTasks([])` and is intentionally invisible.

| Spec row | File:line | Reality |
|---|---|---|
| 4 | `pages/EpicDetailPage.tsx` L41–49 | `fetchTasks` callback, falls back to `setTasks([])` |
| 8 | `pages/SprintDetailPage.tsx` L51–59 | `fetchTasks` callback, falls back to `setTasks([])` |
| 12 | `pages/ProjectDetailPage.tsx` L47–55 | `fetchTasks` callback, falls back to `setTasks([])` |

These remain as-is. Same exclusion category, same reason.

### Corrected handler / fallback wording (6 sites)

The grep guessed verb-noun pairs from filename context; the actual handlers were different. Fallbacks were corrected during implementation:

| Spec row | File | Spec said | Actual handler | Actual fallback |
|---|---|---|---|---|
| 5 | `EpicDetailPage.tsx` L82 | "Failed to update epic" | `handleTransition` | `Failed to update task status` |
| 6 | `SprintsPage.tsx` L113 | "Failed to create sprint" | `handleToggleStatus` | `Failed to update sprint status` |
| 9 | `SprintDetailPage.tsx` L92 | "Failed to update sprint" | `handleTransition` | `Failed to update task status` |
| 10 | `ProjectsPage.tsx` L101 | "Failed to create project" | `handleToggleStatus` | `Failed to update project status` |
| 13 | `ProjectDetailPage.tsx` L109 | "Failed to update project" | `handleTransition` | `Failed to update task status` |
| 14 | `EpicsPage.tsx` L114 | "Failed to create epic" | `handleToggleStatus` | `Failed to update epic status` |

### Page-level smoke tests skipped

The spec called for two smoke tests added to existing `TaskDetailPage` and `BoardPage` test suites. **No such suites exist in the repo** — `apps/gui/src/` has only `lib/sentinel-display.test.ts`, `lib/task-diff.test.ts`, and the new `lib/toast.test.ts`. The spec already contemplates skipping when host suites are unfit ("If a collision exists, skip that site's unit test and rely on the module unit tests plus browser verification" — Layer 2). No-host-file is a stronger version of collision, so the same escape hatch was applied.

Coverage in this pass:
- **Layer 1 (module unit tests):** 5 cases pin the unwrap policy in `lib/toast.test.ts`. Unchanged from spec.
- **Layer 2 (page smoke tests):** SKIPPED. Not worth creating two new test files (api/router/hook mocking + render setup) for one toast assertion each.
- **Layer 3 (browser verification):** COMPLETED 2026-04-11 against worktree vite dev on `:5177`. Forced `todo → backlog` transition (API rejects with `cannot transition from todo to backlog: transition not permitted`) — red toast surfaced with the API's exact message verbatim, confirming `extractMessage` unwrapped `err.message` rather than using the hardcoded fallback. Comment-rejection path not forcible from UI (client-side validation blocks empty submission before reaching the handler); the handler's wrap-pattern is identical to the verified transition sites and is covered by the module unit tests.

When real page-level test infrastructure lands later, the unit tests can be re-introduced as a follow-up PR.

### Collateral backend issue captured separately

The `todo → backlog` rejection observed during verification is a **backend transition state machine** concern (the GUI correctly surfaced a valid API error). Parked in `.agentrc/boot-prompt.md` under backend follow-ups (main commit `32a8e57`, 2026-04-11) — NOT in scope for this PR.
