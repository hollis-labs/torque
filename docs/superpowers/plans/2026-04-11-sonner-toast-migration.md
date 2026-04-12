# Sonner Toast Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace 13 silent-catch mutation handlers across the GUI with visible error toasts, routed through a single `@/lib/toast` module so future error-handling changes land in one file.

**Architecture:** Create `apps/gui/src/lib/toast.ts` as the sole sonner consumer (`notifyError` + `notifySuccess` exports, sealed surface). Each call site keeps its existing state-update logic verbatim and adds one `notifyError(err, fallback)` call inside its catch. New unit-test file pins the unwrap policy. Page-level smoke tests are skipped (no host suites exist; spec's collision-skip rule applies). Browser verification confirms both happy + error paths.

**Tech Stack:** React 19 SPA (Vite), TypeScript strict, sonner (already mounted in `App.tsx`), vitest, `@/lib/api` client.

**Spec:** `docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md` (will be amended in Task 13 with verified inventory + skip-smoke-tests delta).

**Branch / worktree:** `feat/sonner-toast-migration` in `.worktrees/sonner-toast-migration/`.

---

## File Map

- **Create:** `apps/gui/src/lib/toast.ts` — sealed wrapper around sonner. Two exports: `notifyError(err, fallback)`, `notifySuccess(msg)`. Internal `extractMessage(err, fallback)` does the unwrap.
- **Create:** `apps/gui/src/lib/toast.test.ts` — 5 vitest cases, mocks sonner, asserts on `toast.error` / `toast.success` calls.
- **Modify (13 mutation handlers across 9 files):**
  - `apps/gui/src/pages/TaskDetailPage.tsx` — `handleAddComment` (wrap whole body in try/catch), `handleTransition` (add notifyError to existing catch).
  - `apps/gui/src/pages/BoardPage.tsx` — `handleTransition`.
  - `apps/gui/src/pages/EpicDetailPage.tsx` — `handleTransition` (only the L78–85 mutation handler; do **not** touch `fetchTasks` at L41–49).
  - `apps/gui/src/pages/SprintsPage.tsx` — `handleToggleStatus`, `handleDelete`.
  - `apps/gui/src/pages/SprintDetailPage.tsx` — `handleTransition` (only the L88–95 mutation handler; do **not** touch `fetchTasks` at L51–59).
  - `apps/gui/src/pages/ProjectsPage.tsx` — `handleToggleStatus`, `handleDelete`.
  - `apps/gui/src/pages/ProjectDetailPage.tsx` — `handleTransition` (only the L105–112 mutation handler; do **not** touch `fetchTasks` at L47–55).
  - `apps/gui/src/pages/EpicsPage.tsx` — `handleToggleStatus`, `handleDelete`.
  - `apps/gui/src/pages/SettingsPage.tsx` — `handleToggle`.
- **Modify (spec amendment in Task 13):** `docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md` — add "Implementation deltas" section.

## Verified Call Site Inventory (13 sites)

| # | File | Handler | Fallback message |
|---|---|---|---|
| 1 | `TaskDetailPage.tsx` | `handleAddComment` (L156, no existing try/catch) | `Failed to add comment` |
| 2 | `TaskDetailPage.tsx` | `handleTransition` (L162) | `Failed to update task status` |
| 3 | `BoardPage.tsx` | `handleTransition` (L83) | `Failed to update task status` |
| 4 | `EpicDetailPage.tsx` | `handleTransition` (L78) | `Failed to update task status` |
| 5 | `SprintsPage.tsx` | `handleToggleStatus` (L108) | `Failed to update sprint status` |
| 6 | `SprintsPage.tsx` | `handleDelete` (L118) | `Failed to delete sprint` |
| 7 | `SprintDetailPage.tsx` | `handleTransition` (L88) | `Failed to update task status` |
| 8 | `ProjectsPage.tsx` | `handleToggleStatus` (L96) | `Failed to update project status` |
| 9 | `ProjectsPage.tsx` | `handleDelete` (L106) | `Failed to delete project` |
| 10 | `ProjectDetailPage.tsx` | `handleTransition` (L105) | `Failed to update task status` |
| 11 | `EpicsPage.tsx` | `handleToggleStatus` (L109) | `Failed to update epic status` |
| 12 | `EpicsPage.tsx` | `handleDelete` (L119) | `Failed to delete epic` |
| 13 | `SettingsPage.tsx` | `handleToggle` (L25) | `Failed to update setting` |

> **Line numbers** are accurate against branch `feat/sonner-toast-migration` HEAD at plan time. If a step's line numbers don't match because of edits made in earlier tasks, re-grep for the handler signature instead of trusting the number.

## Scope Guards (do NOT drift)

- Do **not** touch `EmptyState variant="error"` views.
- Do **not** touch the `saveError` red banner under `TaskDetailHeader` in `TaskDetailPage.tsx`.
- Do **not** add success toasts (none of the 13 sites get `notifySuccess`).
- Do **not** touch `fetchTasks` catches in `EpicDetailPage`, `SprintDetailPage`, `ProjectDetailPage` — these are list-fetch silent degradation, same exclusion category as the spec's "best-effort prefetch catches."
- Do **not** touch best-effort prefetches in `TaskDetailPage` (L90/L98/L106/L132/L135/L138).
- Do **not** change `App.tsx` `<Toaster />` config.
- Do **not** install new packages.
- Do **not** create a `useAction` hook.
- Do **not** import `toast` directly from `sonner` anywhere outside `lib/toast.ts`.

---

## Task 1: Create `lib/toast.ts` module + unit tests (TDD)

**Files:**
- Create: `apps/gui/src/lib/toast.ts`
- Create: `apps/gui/src/lib/toast.test.ts`

- [ ] **Step 1: Write the failing test file**

Create `apps/gui/src/lib/toast.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { notifyError, notifySuccess } from './toast'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

import { toast } from 'sonner'

describe('notifyError', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uses Error.message when err is an Error with a non-empty message', () => {
    notifyError(new Error('boom'), 'fallback')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('boom')
  })

  it('uses the string itself when err is a non-empty string', () => {
    notifyError('explicit string', 'fallback')
    expect(toast.error).toHaveBeenCalledWith('explicit string')
  })

  it('uses the fallback when err is null/undefined/object/number', () => {
    notifyError(null, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError(undefined, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError({}, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError(42, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')
  })

  it('uses the fallback when err is an Error with an empty message', () => {
    notifyError(new Error(''), 'fallback')
    expect(toast.error).toHaveBeenCalledWith('fallback')
  })
})

describe('notifySuccess', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('forwards the message to toast.success', () => {
    notifySuccess('all good')
    expect(toast.success).toHaveBeenCalledTimes(1)
    expect(toast.success).toHaveBeenCalledWith('all good')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run from `apps/gui/`:

```bash
npm run test:run -- src/lib/toast.test.ts
```

Expected: FAIL with module-not-found / import error for `./toast`.

- [ ] **Step 3: Create the module**

Create `apps/gui/src/lib/toast.ts`:

```ts
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

- [ ] **Step 4: Run tests to verify they pass**

```bash
npm run test:run -- src/lib/toast.test.ts
```

Expected: 5 tests passing in `src/lib/toast.test.ts`. (Two `describe` blocks: 4 cases for `notifyError`, 1 case for `notifySuccess`.)

- [ ] **Step 5: Run the full suite to confirm no regression**

```bash
npm run test:run
```

Expected: 31 tests passing (26 baseline + 5 new), 0 failures.

- [ ] **Step 6: Run typecheck**

```bash
npm run build
```

Expected: clean tsc + vite build, no errors.

- [ ] **Step 7: Commit**

```bash
git add apps/gui/src/lib/toast.ts apps/gui/src/lib/toast.test.ts
git commit -m "feat(gui): add lib/toast wrapper around sonner

Sealed surface (notifyError, notifySuccess) so call sites never import
sonner directly. Future structured-error decoding lands in one file."
```

---

## Task 2: Migrate `TaskDetailPage` (sites 1 + 2)

**Files:**
- Modify: `apps/gui/src/pages/TaskDetailPage.tsx`

- [ ] **Step 1: Add the import**

At the top of `apps/gui/src/pages/TaskDetailPage.tsx`, alongside the existing `@/lib` imports, add:

```ts
import { notifyError } from '@/lib/toast'
```

Place it next to the other `@/lib/...` imports to keep import groups consistent.

- [ ] **Step 2: Wrap `handleAddComment` in try/catch (site 1)**

Find this function (around L156–160):

```ts
async function handleAddComment(content: string) {
  if (!id) return
  const comment = await api.addComment(id, content)
  setComments((prev) => [...(prev ?? []), comment])
}
```

Replace with:

```ts
async function handleAddComment(content: string) {
  if (!id) return
  try {
    const comment = await api.addComment(id, content)
    setComments((prev) => [...(prev ?? []), comment])
  } catch (err) {
    notifyError(err, 'Failed to add comment')
  }
}
```

**Critical:** `setComments` MUST stay inside the try block — moving it outside would append a ghost comment on failure.

- [ ] **Step 3: Add notifyError to `handleTransition` (site 2)**

Find this function (around L162–170):

```ts
async function handleTransition(status: TaskStatus) {
  if (!id) return
  try {
    const updated = await api.transitionTask(id, status)
    setTask(updated)
  } catch {
    // no-op — the badge doesn't change, user sees no update
  }
}
```

Replace the catch:

```ts
async function handleTransition(status: TaskStatus) {
  if (!id) return
  try {
    const updated = await api.transitionTask(id, status)
    setTask(updated)
  } catch (err) {
    notifyError(err, 'Failed to update task status')
  }
}
```

- [ ] **Step 4: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

Expected: all tests still passing, build clean.

- [ ] **Step 5: Commit**

```bash
git add apps/gui/src/pages/TaskDetailPage.tsx
git commit -m "feat(gui): surface task detail mutation errors via toast

handleAddComment now wraps body in try/catch so failures are visible.
handleTransition replaces silent catch with notifyError."
```

---

## Task 3: Migrate `BoardPage` (site 3)

**Files:**
- Modify: `apps/gui/src/pages/BoardPage.tsx`

- [ ] **Step 1: Add the import**

Add to the import block:

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleTransition`**

Find (around L83–90):

```ts
async function handleTransition(id: string, status: TaskStatus) {
  try {
    await api.transitionTask(id, status)
    fetchTasks()
  } catch {
    // silently fail — user will see no change
  }
}
```

Replace catch:

```ts
async function handleTransition(id: string, status: TaskStatus) {
  try {
    await api.transitionTask(id, status)
    fetchTasks()
  } catch (err) {
    notifyError(err, 'Failed to update task status')
  }
}
```

- [ ] **Step 3: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/BoardPage.tsx
git commit -m "feat(gui): surface board page transition errors via toast"
```

---

## Task 4: Migrate `EpicDetailPage` (site 4)

**Files:**
- Modify: `apps/gui/src/pages/EpicDetailPage.tsx`

> **Do NOT touch** `fetchTasks` at L41–49. That catch falls back to `setTasks([])` and is the same exclusion category as best-effort prefetches.

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleTransition`**

Find (around L78–85):

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch (err) {
    notifyError(err, 'Failed to update task status')
  }
}
```

- [ ] **Step 3: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/EpicDetailPage.tsx
git commit -m "feat(gui): surface epic detail transition errors via toast"
```

---

## Task 5: Migrate `SprintsPage` (sites 5 + 6)

**Files:**
- Modify: `apps/gui/src/pages/SprintsPage.tsx`

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleToggleStatus` (site 5)**

Find (around L108–116):

```ts
async function handleToggleStatus(sprint: Sprint) {
  try {
    const newStatus: ContainerStatus = sprint.status === 'active' ? 'inactive' : 'active'
    await api.transitionSprint(sprint.id, newStatus)
    fetchSprints()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleToggleStatus(sprint: Sprint) {
  try {
    const newStatus: ContainerStatus = sprint.status === 'active' ? 'inactive' : 'active'
    await api.transitionSprint(sprint.id, newStatus)
    fetchSprints()
  } catch (err) {
    notifyError(err, 'Failed to update sprint status')
  }
}
```

- [ ] **Step 3: Update `handleDelete` (site 6)**

Find (around L118–125):

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteSprint(id)
    fetchSprints()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteSprint(id)
    fetchSprints()
  } catch (err) {
    notifyError(err, 'Failed to delete sprint')
  }
}
```

- [ ] **Step 4: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add apps/gui/src/pages/SprintsPage.tsx
git commit -m "feat(gui): surface sprint list mutation errors via toast"
```

---

## Task 6: Migrate `SprintDetailPage` (site 7)

**Files:**
- Modify: `apps/gui/src/pages/SprintDetailPage.tsx`

> **Do NOT touch** `fetchTasks` at L51–59 (best-effort list fetch).

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleTransition`**

Find (around L88–95):

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch (err) {
    notifyError(err, 'Failed to update task status')
  }
}
```

- [ ] **Step 3: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/SprintDetailPage.tsx
git commit -m "feat(gui): surface sprint detail transition errors via toast"
```

---

## Task 7: Migrate `ProjectsPage` (sites 8 + 9)

**Files:**
- Modify: `apps/gui/src/pages/ProjectsPage.tsx`

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleToggleStatus` (site 8)**

Find (around L96–104):

```ts
async function handleToggleStatus(project: Project) {
  try {
    const newStatus: ContainerStatus = project.status === 'active' ? 'inactive' : 'active'
    await api.updateProject(project.id, { status: newStatus })
    fetchProjects()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleToggleStatus(project: Project) {
  try {
    const newStatus: ContainerStatus = project.status === 'active' ? 'inactive' : 'active'
    await api.updateProject(project.id, { status: newStatus })
    fetchProjects()
  } catch (err) {
    notifyError(err, 'Failed to update project status')
  }
}
```

- [ ] **Step 3: Update `handleDelete` (site 9)**

Find (around L106–113):

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteProject(id)
    fetchProjects()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteProject(id)
    fetchProjects()
  } catch (err) {
    notifyError(err, 'Failed to delete project')
  }
}
```

- [ ] **Step 4: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add apps/gui/src/pages/ProjectsPage.tsx
git commit -m "feat(gui): surface project list mutation errors via toast"
```

---

## Task 8: Migrate `ProjectDetailPage` (site 10)

**Files:**
- Modify: `apps/gui/src/pages/ProjectDetailPage.tsx`

> **Do NOT touch** `fetchTasks` at L47–55 (best-effort list fetch).

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleTransition`**

Find (around L105–112):

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleTransition(taskId: string, status: TaskStatus) {
  try {
    await api.transitionTask(taskId, status)
    fetchTasks()
  } catch (err) {
    notifyError(err, 'Failed to update task status')
  }
}
```

- [ ] **Step 3: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/ProjectDetailPage.tsx
git commit -m "feat(gui): surface project detail transition errors via toast"
```

---

## Task 9: Migrate `EpicsPage` (sites 11 + 12)

**Files:**
- Modify: `apps/gui/src/pages/EpicsPage.tsx`

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleToggleStatus` (site 11)**

Find (around L109–117):

```ts
async function handleToggleStatus(epic: Epic) {
  try {
    const newStatus: ContainerStatus = epic.status === 'active' ? 'inactive' : 'active'
    await api.updateEpic(epic.id, { status: newStatus })
    fetchEpics()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleToggleStatus(epic: Epic) {
  try {
    const newStatus: ContainerStatus = epic.status === 'active' ? 'inactive' : 'active'
    await api.updateEpic(epic.id, { status: newStatus })
    fetchEpics()
  } catch (err) {
    notifyError(err, 'Failed to update epic status')
  }
}
```

- [ ] **Step 3: Update `handleDelete` (site 12)**

Find (around L119–126):

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteEpic(id)
    fetchEpics()
  } catch {
    // no-op
  }
}
```

Replace catch:

```ts
async function handleDelete(id: string) {
  try {
    await api.deleteEpic(id)
    fetchEpics()
  } catch (err) {
    notifyError(err, 'Failed to delete epic')
  }
}
```

- [ ] **Step 4: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add apps/gui/src/pages/EpicsPage.tsx
git commit -m "feat(gui): surface epic list mutation errors via toast"
```

---

## Task 10: Migrate `SettingsPage` (site 13)

**Files:**
- Modify: `apps/gui/src/pages/SettingsPage.tsx`

- [ ] **Step 1: Add the import**

```ts
import { notifyError } from '@/lib/toast'
```

- [ ] **Step 2: Update `handleToggle`**

Find (around L25–37):

```ts
async function handleToggle(key: keyof FeatureFlags, value: boolean) {
  if (!flags) return
  setSaving(key)
  const updated = { ...flags, [key]: value }
  setFlags(updated) // optimistic
  try {
    await api.setSetting(`feature-flags.${key}`, String(value))
  } catch {
    setFlags(flags) // revert on error
  } finally {
    setSaving(null)
  }
}
```

Replace catch — preserve the optimistic revert, add notifyError after it:

```ts
async function handleToggle(key: keyof FeatureFlags, value: boolean) {
  if (!flags) return
  setSaving(key)
  const updated = { ...flags, [key]: value }
  setFlags(updated) // optimistic
  try {
    await api.setSetting(`feature-flags.${key}`, String(value))
  } catch (err) {
    setFlags(flags) // revert on error
    notifyError(err, 'Failed to update setting')
  } finally {
    setSaving(null)
  }
}
```

**Critical:** `setFlags(flags)` (revert) MUST stay before `notifyError`. Order matters for visual consistency — UI rolls back first, then the toast appears.

- [ ] **Step 3: Run tests + typecheck**

```bash
npm run test:run && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/pages/SettingsPage.tsx
git commit -m "feat(gui): surface settings toggle errors via toast"
```

---

## Task 11: Verify no direct sonner imports leaked

**Files:** read-only sweep.

- [ ] **Step 1: Confirm sonner is only imported in `lib/toast.ts` and `components/ui/sonner.tsx`**

Run from `apps/gui/`:

```bash
git grep -n "from 'sonner'" src/
```

Expected exact output (or equivalent — paths only):

```
src/components/ui/sonner.tsx:...
src/lib/toast.ts:...
src/lib/toast.test.ts:...
```

If any other file shows up, it's a leak — open that file and replace the direct sonner import with `notifyError`/`notifySuccess` from `@/lib/toast`, then re-run the grep.

- [ ] **Step 2: Confirm `notifyError` is wired into all 13 sites**

```bash
git grep -n "notifyError" src/pages/
```

Expected: at least 13 lines across 9 files (TaskDetailPage has 2, SprintsPage 2, ProjectsPage 2, EpicsPage 2, the rest 1 each).

If a count is off, find the missing site and fix it before proceeding.

- [ ] **Step 3: Final full-suite test + build**

```bash
npm run test:run && npm run build
```

Expected: 31 tests passing (26 baseline + 5 new), build clean.

- [ ] **Step 4: No commit needed** — this task is verification only.

---

## Task 12: Browser verification (manual, cerberus)

**Goal:** Force a happy path (no toast) and an error path (red toast) through the GUI on `:5175`. Watch cerberus logs in parallel.

> **Important:** This task is performed against the worktree's frontend. Cerberus is configured against the main checkout's frontend at `:5175`. Two safe options below.

- [ ] **Step 1: Pick a verification mode**

**Option A — Cerberus on main checkout (lighter):**
After Task 11, do not switch checkouts; instead `cd` back to the main checkout for verification:

```bash
# from worktree
cd /Users/chrispian/Projects-apps/clockwork-manifold
```

You will not see the new code in the running cerberus instance, so this option only works AFTER the PR is merged. Skip Option A unless you've already merged.

**Option B — Run vite dev directly against the worktree (recommended pre-merge):**
From the worktree's `apps/gui/`:

```bash
npm run dev
```

This starts a second vite dev server on a different port (vite auto-picks if 5173 is taken). Note the URL it prints. The API stays the cerberus-managed instance on `:8990` — no changes needed there.

- [ ] **Step 2: Happy path — valid task transition**

In the browser:
1. Navigate to the Board page.
2. Pick any task in `todo` and transition it to `doing` (via the row action menu or drag, whichever the page supports).
3. Confirm: badge updates, **no toast** appears (errors-only policy).

Repeat for one transition on the Task Detail page.

- [ ] **Step 3: Error path — force a transition rejection**

In the browser, force a 4xx by triggering a transition that the API rejects. Two ways:
1. **Easiest:** transition a `done` task back to `doing` via the API directly to rule out the GUI's optimistic guarding, then attempt the same transition through the GUI. If the GUI still allows the click, it should error and you'll see a red toast bottom-right.
2. **Fallback:** use browser devtools to call `fetch('/api/v1/tasks/<id>/transition', { method: 'POST', body: JSON.stringify({status: 'INVALID_STATUS'}), headers: {'content-type': 'application/json'}})` and confirm the daemon rejects it with a real error message. Then trigger the same invalid transition path in the GUI handler if reachable from the UI.

If neither path is reachable without destructive actions (kill API, corrupt DB, etc.), say so explicitly in the verification report — do **not** claim success.

Confirm the toast contains the API's error message verbatim, not the fallback string.

- [ ] **Step 4: Error path — force comment rejection**

Open any task detail page, attempt to add a comment with content the API rejects (empty body, oversized, whatever the validation rule is — check `internal/service/comment.go` if uncertain). Confirm a red toast with the API's message.

- [ ] **Step 5: Stream cerberus logs during verification**

In a parallel terminal:

```bash
cerberus logs clockwork-frontend --follow
cerberus logs clockwork-api --follow
```

(Two terminals, or one with `&`.) Note any unexpected errors or warnings.

- [ ] **Step 6: Write a one-paragraph verification report**

Capture exactly:
- What was tested (happy + each error path).
- What was observed (toast text, badge state, log lines).
- What could **not** be reliably reproduced and why.

Save the report as a comment for the PR description in Task 14 — do **not** commit it as a file.

- [ ] **Step 7: No commit needed** — verification is observational.

---

## Task 13: Amend the spec with implementation deltas

**Files:**
- Modify: `docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md`

- [ ] **Step 1: Append a deltas section**

At the end of the spec file, add:

```markdown

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

The spec called for two smoke tests added to existing `TaskDetailPage` and `BoardPage` test suites. **No such suites exist in the repo** — `apps/gui/src/` has only `lib/sentinel-display.test.ts` and `lib/task-diff.test.ts`. The spec already contemplates skipping when host suites are unfit ("If a collision exists, skip that site's unit test and rely on the module unit tests plus browser verification" — Layer 2). No-host-file is a stronger version of collision, so the same escape hatch was applied.

Coverage in this pass:
- **Layer 1 (module unit tests):** 5 cases pin the unwrap policy in `lib/toast.test.ts`. Unchanged from spec.
- **Layer 2 (page smoke tests):** SKIPPED. Not worth creating two new test files (api/router/hook mocking + render setup) for one toast assertion each.
- **Layer 3 (browser verification):** UNCHANGED. Forced both happy and error paths through cerberus-managed dev environment.

When real page-level test infrastructure lands later, the unit tests can be re-introduced as a follow-up PR.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md
git commit -m "docs(spec): record sonner migration implementation deltas

13 verified mutation sites (down from grep-derived 16). 3 list-fetch
catches reclassified as best-effort prefetch exclusions. Page smoke
tests skipped due to absent host suites."
```

---

## Task 14: Push branch and open PR

**Files:** none (git/gh only).

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/sonner-toast-migration
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "feat(gui): sonner toast migration — surface mutation errors" --body "$(cat <<'EOF'
## Summary
- Adds `apps/gui/src/lib/toast.ts` as the single sonner consumer (`notifyError`, `notifySuccess` exports, sealed surface).
- Migrates 13 silent-catch mutation handlers across 9 pages to surface API errors as red toasts. Errors-only this pass — no success toasts.
- 5 unit tests pin the unwrap policy. Page-level smoke tests skipped (no host suites — see spec deltas).

## Spec
`docs/superpowers/specs/2026-04-11-sonner-toast-migration-design.md` — approved 2026-04-11. Implementation deltas appended in this PR (3 list-fetch catches reclassified as best-effort exclusions; corrected handler/fallback wording on 6 sites).

## Test plan
- [ ] `npm run test:run` (apps/gui) — 31 passing
- [ ] `npm run build` (apps/gui) — clean tsc + vite build
- [ ] Browser: valid task transition → no toast, badge updates
- [ ] Browser: invalid task transition → red toast bottom-right with API message
- [ ] Browser: invalid comment add → red toast with API message
- [ ] No new direct `sonner` imports (`git grep "from 'sonner'" src/` returns only `lib/toast.ts`, `lib/toast.test.ts`, `components/ui/sonner.tsx`)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Paste the verification report from Task 12 Step 6 as a PR comment**

```bash
gh pr comment --body "Verification report: <paste from Task 12 step 6>"
```

- [ ] **Step 4: Wait for Copilot review, address any feedback in follow-up commits, then merge.**

Use `superpowers:receiving-code-review` skill if Copilot leaves substantive comments.

---

## Self-review checklist

1. **Spec coverage** — Every section of the spec has a task: `lib/toast.ts` module + tests (Task 1), 13 call sites (Tasks 2–10), no direct sonner leak guard (Task 11), browser verification (Task 12), spec amendment (Task 13), PR (Task 14). Smoke tests are explicitly skipped with the escape-hatch justification recorded in the spec amendment.

2. **Type consistency** — Module exports are `notifyError(err: unknown, fallback: string): void` and `notifySuccess(message: string): void`. Every call site uses `notifyError(err, '<fallback>')` exactly.

3. **Placeholder scan** — No "TBD"/"add error handling"/"similar to Task N" anywhere. Every step has either runnable code, a runnable command, or an explicit observation instruction.

4. **Scope guards** — `fetchTasks` exclusions called out at the file level in Tasks 4, 6, 8. Best-effort prefetch exclusions in `TaskDetailPage` are inherited from the spec (not touched in Task 2). The `saveError` banner is not in the migrated handlers and is therefore implicitly preserved.
