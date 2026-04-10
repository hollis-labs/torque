# Task Detail/Edit Page Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `TaskDetailPage` to match the tasks home HUD aesthetic with `?edit=1` whole-page edit mode covering Tiers 1–4 of canonical task fields.

**Architecture:** Single page with conditional rendering — `TaskDetailPage` owns fetch, draft state, and save/cancel flow. Five section components (`TaskDetailHeader`, `TaskProperties`, `ExecutionContext`, `LifecycleRules`, `DeliverablesAndDeps`) each accept an `editing` prop and render view or edit UI. A shared `DetailSection` layout shell (accent bar, collapse, summary) wraps the four field sections. Pure logic (diff computation, sentinel display) lives in `lib/` with vitest-covered tests.

**Tech Stack:** React 19, Vite, TypeScript, Tailwind CSS 4, shadcn/ui, react-router-dom v7. Vitest (new, added in Task 1) for pure-logic tests.

**Spec:** `docs/superpowers/specs/2026-04-09-task-detail-edit-rebuild-design.md`

**Working directory for all commands:** `apps/gui/`

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `apps/gui/vitest.config.ts` | Vitest config |
| `apps/gui/src/lib/task-diff.ts` | Compute PATCH diff from original + draft task, map tags |
| `apps/gui/src/lib/task-diff.test.ts` | Tests for task-diff |
| `apps/gui/src/lib/sentinel-display.ts` | Format null/-1/0/positive sentinels for display |
| `apps/gui/src/lib/sentinel-display.test.ts` | Tests for sentinel-display |
| `apps/gui/src/components/domain/detail-section.tsx` | Shared panel shell (accent bar + collapse) |
| `apps/gui/src/components/domain/task-detail-header.tsx` | HUD task detail header with view/edit modes |
| `apps/gui/src/components/domain/task-properties.tsx` | Tier 1 properties grid |
| `apps/gui/src/components/domain/execution-context.tsx` | Tier 2 execution context panel |
| `apps/gui/src/components/domain/lifecycle-rules.tsx` | Tier 3 lifecycle rules panel |
| `apps/gui/src/components/domain/deliverables-deps.tsx` | Tier 4 deliverables & dependencies panel |

### Modified files

| File | Change |
|------|--------|
| `apps/gui/package.json` | Add vitest + test scripts |
| `apps/gui/src/pages/TaskDetailPage.tsx` | Full rewrite |

---

## Task 1: Add vitest test infrastructure

**Why first:** Tasks 2 and 3 are TDD pure-logic work and need a test runner. Tasks 4+ don't need tests but the infra must exist.

**Files:**
- Create: `apps/gui/vitest.config.ts`
- Modify: `apps/gui/package.json`
- Create: `apps/gui/src/lib/smoke.test.ts` (throwaway smoke test, deleted at end of task)

- [ ] **Step 1.1: Install vitest**

Working directory: `apps/gui/`

Run: `npm install --save-dev vitest@^2.1.0`
Expected: package added to `devDependencies`, no errors.

- [ ] **Step 1.2: Add test scripts to `apps/gui/package.json`**

In the `"scripts"` object, add two entries (alongside the existing `dev`, `build`, `lint`, `preview`):

```json
    "test": "vitest",
    "test:run": "vitest run"
```

The full `scripts` block should look like:

```json
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "lint": "eslint .",
    "preview": "vite preview",
    "test": "vitest",
    "test:run": "vitest run"
  },
```

- [ ] **Step 1.3: Create `apps/gui/vitest.config.ts`**

```typescript
import { defineConfig } from 'vitest/config'
import path from 'node:path'

export default defineConfig({
  test: {
    globals: false,
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    environment: 'node',
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})
```

- [ ] **Step 1.4: Write a throwaway smoke test**

Create `apps/gui/src/lib/smoke.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'

describe('vitest smoke', () => {
  it('runs', () => {
    expect(1 + 1).toBe(2)
  })
})
```

- [ ] **Step 1.5: Run the smoke test**

Working directory: `apps/gui/`

Run: `npm run test:run`
Expected: 1 passing test. Exit 0.

If it fails, fix vitest.config.ts / package.json before continuing.

- [ ] **Step 1.6: Delete the smoke test**

Delete `apps/gui/src/lib/smoke.test.ts`. It was just to verify the setup worked.

- [ ] **Step 1.7: Commit**

```bash
git add apps/gui/package.json apps/gui/package-lock.json apps/gui/vitest.config.ts
git commit -m "$(cat <<'EOF'
chore(gui): add vitest for pure-logic unit tests

Project 3 prep — logic helpers for the task detail rebuild
(diff computation, sentinel display) are TDD'd. Visual
components keep manual browser verification.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Task diff helper (TDD)

**Purpose:** Pure function that produces a minimal PATCH payload from the original task and the current draft. Handles the `Tag[]` → `string[]` mapping that the API expects.

**Files:**
- Create: `apps/gui/src/lib/task-diff.test.ts`
- Create: `apps/gui/src/lib/task-diff.ts`

- [ ] **Step 2.1: Write failing tests**

Create `apps/gui/src/lib/task-diff.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'
import { computeTaskDiff } from './task-diff'
import type { Task, Tag } from './types'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'tsk_test',
    title: 'Original title',
    description: 'Original description',
    status: 'todo',
    priority: 2,
    tags: [],
    manual: false,
    executor: 'cli',
    agent_profile: 'default',
    working_dir: '',
    tools: [],
    permissions: {},
    environment: {},
    system_prompt: '',
    files: [],
    cost_budget: null,
    max_retries: 3,
    max_duration_ms: null,
    token_budget: null,
    on_done: 'close',
    on_fail: 'retry',
    on_review: 'pause',
    on_done_merge: 'none',
    escalation_chain: [],
    quality_gates: [],
    deliverables: [],
    deliverable_preset: '',
    depends_on: [],
    blocked_reason: '',
    metadata: {},
    sprint_id: null,
    project_id: null,
    epic_id: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeTag(slug: string, name?: string): Tag {
  return {
    slug,
    name: name ?? slug,
    description: '',
    color: 'zinc',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

describe('computeTaskDiff', () => {
  it('returns empty object when nothing changed', () => {
    const task = makeTask()
    const diff = computeTaskDiff(task, task)
    expect(diff).toEqual({})
  })

  it('returns only fields that changed', () => {
    const original = makeTask({ title: 'Original', priority: 2 })
    const draft = { ...original, title: 'Updated' }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ title: 'Updated' })
  })

  it('maps tags from Tag[] to string[] of tag names', () => {
    const original = makeTask({ tags: [makeTag('bug')] })
    const draft = { ...original, tags: [makeTag('bug'), makeTag('urgent', 'Urgent')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tags: ['bug', 'Urgent'] })
  })

  it('detects no tag change when tag slugs match', () => {
    const original = makeTask({ tags: [makeTag('bug'), makeTag('urgent')] })
    const draft = { ...original, tags: [makeTag('bug'), makeTag('urgent')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({})
  })

  it('detects tag removal', () => {
    const original = makeTask({ tags: [makeTag('bug'), makeTag('urgent')] })
    const draft = { ...original, tags: [makeTag('bug')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tags: ['bug'] })
  })

  it('detects string array change (tools)', () => {
    const original = makeTask({ tools: ['bash', 'git'] })
    const draft = { ...original, tools: ['bash', 'git', 'grep'] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tools: ['bash', 'git', 'grep'] })
  })

  it('detects record change (environment)', () => {
    const original = makeTask({ environment: { NODE_ENV: 'test' } })
    const draft = { ...original, environment: { NODE_ENV: 'prod' } }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ environment: { NODE_ENV: 'prod' } })
  })

  it('detects nullable sentinel changes (cost_budget)', () => {
    const original = makeTask({ cost_budget: null })
    const draft = { ...original, cost_budget: -1 }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ cost_budget: -1 })
  })

  it('excludes id, created_at, updated_at, status from the diff', () => {
    const original = makeTask()
    const draft = {
      ...original,
      id: 'tsk_other',
      status: 'doing' as const,
      created_at: '2026-02-01T00:00:00Z',
      updated_at: '2026-02-01T00:00:00Z',
      title: 'Changed',
    }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ title: 'Changed' })
  })
})
```

- [ ] **Step 2.2: Run tests to verify they fail**

Working directory: `apps/gui/`

Run: `npm run test:run -- task-diff`
Expected: FAIL — "Cannot find module './task-diff'" or similar.

- [ ] **Step 2.3: Implement `computeTaskDiff`**

Create `apps/gui/src/lib/task-diff.ts`:

```typescript
import type { Task, Tag } from './types'

/**
 * Fields that are never sent in a task update PATCH body.
 * - id/created_at/updated_at: server-managed
 * - status: must use /transition endpoint, not PATCH
 * - tags: handled separately (Tag[] -> string[] mapping)
 */
const EXCLUDED_FIELDS = new Set([
  'id',
  'status',
  'created_at',
  'updated_at',
  'tags',
])

export type TaskUpdatePayload = Partial<Omit<Task, 'tags'>> & { tags?: string[] }

/**
 * Computes the minimal PATCH payload to send to PATCH /tasks/:id.
 *
 * Compares each field of `original` against `draft` using a deep-equality
 * check for arrays/objects and strict equality for primitives. Only fields
 * that differ are included in the result.
 *
 * Tags are a special case: the API accepts `string[]` of tag names, but the
 * Task type carries `Tag[]`. We compare by slug and map to names on change.
 */
export function computeTaskDiff(original: Task, draft: Task): TaskUpdatePayload {
  const diff: TaskUpdatePayload = {}

  for (const key of Object.keys(draft) as (keyof Task)[]) {
    if (EXCLUDED_FIELDS.has(key as string)) continue
    const a = original[key]
    const b = draft[key]
    if (!deepEqual(a, b)) {
      // Assign through unknown to satisfy strict typing — we've already
      // confirmed the key is not one of the excluded/special-cased fields.
      (diff as Record<string, unknown>)[key] = b
    }
  }

  if (!tagsEqual(original.tags, draft.tags)) {
    diff.tags = draft.tags.map((t) => t.name)
  }

  return diff
}

function tagsEqual(a: Tag[], b: Tag[]): boolean {
  if (a.length !== b.length) return false
  const aSlugs = a.map((t) => t.slug).sort()
  const bSlugs = b.map((t) => t.slug).sort()
  return aSlugs.every((s, i) => s === bSlugs[i])
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a === null || b === null) return false
  if (typeof a !== typeof b) return false
  if (typeof a !== 'object') return false
  if (Array.isArray(a) !== Array.isArray(b)) return false

  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false
    return a.every((v, i) => deepEqual(v, b[i]))
  }

  const aObj = a as Record<string, unknown>
  const bObj = b as Record<string, unknown>
  const aKeys = Object.keys(aObj)
  const bKeys = Object.keys(bObj)
  if (aKeys.length !== bKeys.length) return false
  return aKeys.every((k) => deepEqual(aObj[k], bObj[k]))
}
```

- [ ] **Step 2.4: Run tests to verify they pass**

Working directory: `apps/gui/`

Run: `npm run test:run -- task-diff`
Expected: All 9 tests pass. Exit 0.

- [ ] **Step 2.5: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: No errors in `task-diff.ts` or `task-diff.test.ts`.

- [ ] **Step 2.6: Commit**

```bash
git add apps/gui/src/lib/task-diff.ts apps/gui/src/lib/task-diff.test.ts
git commit -m "$(cat <<'EOF'
feat(gui): add computeTaskDiff helper for minimal PATCH payloads

Pure function that diffs original vs draft task and maps Tag[]
to the string[] shape the API expects. Excludes id, status,
created_at, updated_at from the diff.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Sentinel display helper (TDD)

**Purpose:** Format the three sentinel-using numeric fields (`cost_budget`, `max_duration_ms`, `token_budget`) for view mode. `null` → "default", `-1` → "unlimited", `0` → "0" (cost only), positive → formatted value.

**Files:**
- Create: `apps/gui/src/lib/sentinel-display.test.ts`
- Create: `apps/gui/src/lib/sentinel-display.ts`

- [ ] **Step 3.1: Write failing tests**

Create `apps/gui/src/lib/sentinel-display.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'
import {
  formatCostBudget,
  formatDurationMs,
  formatTokenBudget,
} from './sentinel-display'

describe('formatCostBudget', () => {
  it('null → default', () => {
    expect(formatCostBudget(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatCostBudget(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('0 → $0.00', () => {
    expect(formatCostBudget(0)).toEqual({ label: '$0.00', mode: 'value' })
  })
  it('5 → $5.00', () => {
    expect(formatCostBudget(5)).toEqual({ label: '$5.00', mode: 'value' })
  })
  it('12.5 → $12.50', () => {
    expect(formatCostBudget(12.5)).toEqual({ label: '$12.50', mode: 'value' })
  })
})

describe('formatDurationMs', () => {
  it('null → default', () => {
    expect(formatDurationMs(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatDurationMs(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('1000 → 1s', () => {
    expect(formatDurationMs(1000)).toEqual({ label: '1s', mode: 'value' })
  })
  it('60000 → 1m', () => {
    expect(formatDurationMs(60000)).toEqual({ label: '1m', mode: 'value' })
  })
  it('65000 → 1m 5s', () => {
    expect(formatDurationMs(65000)).toEqual({ label: '1m 5s', mode: 'value' })
  })
  it('3600000 → 1h', () => {
    expect(formatDurationMs(3600000)).toEqual({ label: '1h', mode: 'value' })
  })
  it('3665000 → 1h 1m 5s', () => {
    expect(formatDurationMs(3665000)).toEqual({ label: '1h 1m 5s', mode: 'value' })
  })
  // cost_budget allows explicit 0, duration does not — but the function
  // treats it as a valid positive-or-zero value for display purposes
  it('0 → 0s', () => {
    expect(formatDurationMs(0)).toEqual({ label: '0s', mode: 'value' })
  })
})

describe('formatTokenBudget', () => {
  it('null → default', () => {
    expect(formatTokenBudget(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatTokenBudget(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('1000 → 1,000', () => {
    expect(formatTokenBudget(1000)).toEqual({ label: '1,000', mode: 'value' })
  })
  it('1000000 → 1,000,000', () => {
    expect(formatTokenBudget(1000000)).toEqual({ label: '1,000,000', mode: 'value' })
  })
})
```

- [ ] **Step 3.2: Run tests to verify they fail**

Working directory: `apps/gui/`

Run: `npm run test:run -- sentinel-display`
Expected: FAIL — "Cannot find module './sentinel-display'".

- [ ] **Step 3.3: Implement sentinel-display**

Create `apps/gui/src/lib/sentinel-display.ts`:

```typescript
/**
 * Shape returned by every sentinel formatter. The `mode` field lets callers
 * apply different Tailwind classes depending on whether the value is a real
 * number, "unlimited", or "default" (fallback).
 */
export interface SentinelDisplay {
  label: string
  mode: 'default' | 'unlimited' | 'value'
}

const DEFAULT: SentinelDisplay = { label: 'default', mode: 'default' }
const UNLIMITED: SentinelDisplay = { label: 'unlimited', mode: 'unlimited' }

export function formatCostBudget(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: `$${value.toFixed(2)}`, mode: 'value' }
}

export function formatDurationMs(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: formatDuration(value), mode: 'value' }
}

export function formatTokenBudget(value: number | null): SentinelDisplay {
  if (value === null) return DEFAULT
  if (value === -1) return UNLIMITED
  return { label: value.toLocaleString('en-US'), mode: 'value' }
}

function formatDuration(ms: number): string {
  if (ms === 0) return '0s'
  const hours = Math.floor(ms / 3_600_000)
  const minutes = Math.floor((ms % 3_600_000) / 60_000)
  const seconds = Math.floor((ms % 60_000) / 1000)
  const parts: string[] = []
  if (hours > 0) parts.push(`${hours}h`)
  if (minutes > 0) parts.push(`${minutes}m`)
  if (seconds > 0) parts.push(`${seconds}s`)
  return parts.join(' ') || '0s'
}
```

- [ ] **Step 3.4: Run tests to verify they pass**

Working directory: `apps/gui/`

Run: `npm run test:run -- sentinel-display`
Expected: All 17 tests pass. Exit 0.

- [ ] **Step 3.5: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 3.6: Commit**

```bash
git add apps/gui/src/lib/sentinel-display.ts apps/gui/src/lib/sentinel-display.test.ts
git commit -m "$(cat <<'EOF'
feat(gui): add sentinel-display helpers for nullable numeric fields

Formats cost_budget/max_duration_ms/token_budget for view mode:
null → default, -1 → unlimited, positive → formatted value.
Returns { label, mode } so callers can style each mode.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `DetailSection` shared layout shell

**Purpose:** Layout-only wrapper shared by all four Tier sections. Renders a panel with a colored left-accent bar, optional collapse/expand, and an optional inline summary when collapsed. No field logic — pure presentation.

**Files:**
- Create: `apps/gui/src/components/domain/detail-section.tsx`

- [ ] **Step 4.1: Create `detail-section.tsx`**

Create `apps/gui/src/components/domain/detail-section.tsx`:

```tsx
import { useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

export type DetailSectionAccent = 'blue' | 'violet' | 'amber' | 'red' | 'zinc'

const ACCENT_CLASSES: Record<DetailSectionAccent, string> = {
  blue: 'border-l-blue-500/70',
  violet: 'border-l-violet-500/70',
  amber: 'border-l-amber-500/70',
  red: 'border-l-red-500/70',
  zinc: 'border-l-zinc-500/50',
}

interface DetailSectionProps {
  label: string
  accent: DetailSectionAccent
  children: ReactNode
  /** Default true. Set to false for the always-open Properties section. */
  collapsible?: boolean
  /** Only meaningful when collapsible is true. Default false. */
  defaultOpen?: boolean
  /** Inline summary shown next to the label when collapsed. */
  summary?: string
  /** Tailwind classes appended to the root element. */
  className?: string
}

export function DetailSection({
  label,
  accent,
  children,
  collapsible = true,
  defaultOpen = false,
  summary,
  className,
}: DetailSectionProps) {
  const [open, setOpen] = useState(collapsible ? defaultOpen : true)
  const showChildren = !collapsible || open

  return (
    <section
      className={cn(
        'rounded-md border border-zinc-800/50 border-l-2 bg-zinc-950',
        ACCENT_CLASSES[accent],
        className,
      )}
    >
      {collapsible ? (
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left hover:bg-zinc-900/40 transition-colors"
          aria-expanded={open}
        >
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {label}
          </span>
          <span className="flex items-center gap-2 text-[10px] text-zinc-600">
            {summary && !open && <span className="truncate max-w-md">{summary}</span>}
            <ChevronRight
              className={cn(
                'h-3 w-3 transition-transform',
                open && 'rotate-90',
              )}
            />
          </span>
        </button>
      ) : (
        <div className="px-3 py-2">
          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
            {label}
          </span>
        </div>
      )}

      {showChildren && (
        <div className="border-t border-zinc-800/50 px-3 py-3">
          {children}
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 4.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds with no TS or Vite errors.

- [ ] **Step 4.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 4.4: Commit**

```bash
git add apps/gui/src/components/domain/detail-section.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add DetailSection shared panel shell

Layout-only wrapper with colored left-accent bar, optional
collapse/expand, and inline summary when collapsed. Used by
the Tier 1-4 sections on the rebuilt task detail page.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `TaskProperties` component (Tier 1)

**Purpose:** 3-column grid of Tier 1 fields: executor, agent_profile, working_dir, cost_budget, max_retries, manual, project_id, sprint_id, epic_id. View mode shows label/value pairs; edit mode shows label/input pairs.

**Files:**
- Create: `apps/gui/src/components/domain/task-properties.tsx`

- [ ] **Step 5.1: Create `task-properties.tsx`**

```tsx
import { Link } from 'react-router-dom'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import { formatCostBudget } from '@/lib/sentinel-display'
import { UNLIMITED, type Task, type Project, type Sprint, type Epic } from '@/lib/types'

interface TaskPropertiesProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
  projects: Project[]
  sprints: Sprint[]
  epics: Epic[]
  pickersLoading: boolean
}

export function TaskProperties({
  task,
  editing,
  draft,
  onDraftChange,
  projects,
  sprints,
  epics,
  pickersLoading,
}: TaskPropertiesProps) {
  const source = editing ? draft : task

  return (
    <DetailSection label="Properties" accent="blue" collapsible={false}>
      <dl className="grid grid-cols-3 gap-x-4 gap-y-3">
        <Field label="Executor">
          {editing ? (
            <Input
              value={source.executor}
              onChange={(e) => onDraftChange('executor', e.target.value)}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.executor || '—'}</PlainValue>
          )}
        </Field>

        <Field label="Agent Profile">
          {editing ? (
            <Input
              value={source.agent_profile}
              onChange={(e) => onDraftChange('agent_profile', e.target.value)}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.agent_profile || '—'}</PlainValue>
          )}
        </Field>

        <Field label="Working Dir">
          {editing ? (
            <Input
              value={source.working_dir}
              onChange={(e) => onDraftChange('working_dir', e.target.value)}
              className="h-7 text-[13px] font-mono"
            />
          ) : (
            <MonoValue>{source.working_dir || '—'}</MonoValue>
          )}
        </Field>

        <Field label="Cost Budget">
          {editing ? (
            <SentinelInput
              value={source.cost_budget}
              onChange={(v) => onDraftChange('cost_budget', v)}
            />
          ) : (
            <SentinelValue display={formatCostBudget(source.cost_budget)} />
          )}
        </Field>

        <Field label="Max Retries">
          {editing ? (
            <Input
              type="number"
              min={0}
              value={source.max_retries}
              onChange={(e) => onDraftChange('max_retries', Number(e.target.value))}
              className="h-7 text-[13px]"
            />
          ) : (
            <PlainValue>{source.max_retries}</PlainValue>
          )}
        </Field>

        <Field label="Manual">
          {editing ? (
            <Switch
              checked={source.manual}
              onCheckedChange={(v) => onDraftChange('manual', v)}
            />
          ) : (
            <PlainValue>{source.manual ? 'Yes' : 'No'}</PlainValue>
          )}
        </Field>

        <Field label="Project">
          {editing ? (
            <ContainerPicker
              value={source.project_id}
              onChange={(v) => onDraftChange('project_id', v)}
              options={projects.map((p) => ({ id: p.id, label: p.name }))}
              loading={pickersLoading}
              placeholder="none"
            />
          ) : source.project_id ? (
            <LinkedValue to={`/projects/${source.project_id}`}>
              {projects.find((p) => p.id === source.project_id)?.name ?? source.project_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>

        <Field label="Sprint">
          {editing ? (
            <ContainerPicker
              value={source.sprint_id}
              onChange={(v) => onDraftChange('sprint_id', v)}
              options={sprints.map((s) => ({ id: s.id, label: s.name }))}
              loading={pickersLoading}
              placeholder="none"
            />
          ) : source.sprint_id ? (
            <LinkedValue to={`/sprints/${source.sprint_id}`}>
              {sprints.find((s) => s.id === source.sprint_id)?.name ?? source.sprint_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>

        <Field label="Epic">
          {editing ? (
            <ContainerPicker
              value={source.epic_id}
              onChange={(v) => onDraftChange('epic_id', v)}
              options={epics.map((e) => ({ id: e.id, label: e.name }))}
              loading={pickersLoading}
              placeholder="none"
            />
          ) : source.epic_id ? (
            <LinkedValue to={`/epics/${source.epic_id}`}>
              {epics.find((e) => e.id === source.epic_id)?.name ?? source.epic_id}
            </LinkedValue>
          ) : (
            <NoneValue />
          )}
        </Field>
      </dl>
    </DetailSection>
  )
}

/* ----- small display primitives kept local to this file ----- */

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 min-w-0">
      <dt className="text-[9px] uppercase tracking-[.18em] text-zinc-600">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}

function PlainValue({ children }: { children: React.ReactNode }) {
  return <span className="text-[13px] text-zinc-300">{children}</span>
}

function MonoValue({ children }: { children: React.ReactNode }) {
  return <span className="text-[12px] font-mono text-zinc-300 break-all">{children}</span>
}

function NoneValue() {
  return <span className="text-[13px] italic text-zinc-600">none</span>
}

function LinkedValue({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link to={to} className="text-[13px] text-blue-400 hover:text-blue-300 truncate block">
      {children}
    </Link>
  )
}

function SentinelValue({ display }: { display: ReturnType<typeof formatCostBudget> }) {
  const cls =
    display.mode === 'default'
      ? 'italic text-zinc-600'
      : display.mode === 'unlimited'
        ? 'font-mono text-zinc-400'
        : 'text-zinc-300'
  return <span className={`text-[13px] ${cls}`}>{display.label}</span>
}

function SentinelInput({
  value,
  onChange,
}: {
  value: number | null
  onChange: (v: number | null) => void
}) {
  const mode: 'default' | 'unlimited' | 'value' =
    value === null ? 'default' : value === UNLIMITED ? 'unlimited' : 'value'

  return (
    <div className="flex items-center gap-2">
      <Select
        value={mode}
        onValueChange={(m) => {
          if (m === 'default') onChange(null)
          else if (m === 'unlimited') onChange(UNLIMITED)
          else onChange(0)
        }}
      >
        <SelectTrigger className="h-7 w-28 text-[12px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="default">default</SelectItem>
          <SelectItem value="unlimited">unlimited</SelectItem>
          <SelectItem value="value">value</SelectItem>
        </SelectContent>
      </Select>
      {mode === 'value' && (
        <Input
          type="number"
          step="0.01"
          min={0}
          value={value ?? 0}
          onChange={(e) => onChange(Number(e.target.value))}
          className="h-7 w-24 text-[13px]"
        />
      )}
    </div>
  )
}

function ContainerPicker({
  value,
  onChange,
  options,
  loading,
  placeholder,
}: {
  value: string | null
  onChange: (v: string | null) => void
  options: { id: string; label: string }[]
  loading: boolean
  placeholder: string
}) {
  const NONE_VALUE = '__none__'
  return (
    <Select
      value={value ?? NONE_VALUE}
      onValueChange={(v) => onChange(v === NONE_VALUE ? null : v)}
      disabled={loading}
    >
      <SelectTrigger className="h-7 text-[12px]">
        <SelectValue placeholder={loading ? 'loading...' : placeholder} />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={NONE_VALUE}>{placeholder}</SelectItem>
        {options.map((o) => (
          <SelectItem key={o.id} value={o.id}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
```

- [ ] **Step 5.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds. Note: this file is not imported anywhere yet so TS only checks it in isolation.

- [ ] **Step 5.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 5.4: Commit**

```bash
git add apps/gui/src/components/domain/task-properties.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add TaskProperties Tier 1 section component

3-column grid with view/edit modes for executor, agent_profile,
working_dir, cost_budget (sentinel-aware), max_retries, manual,
project/sprint/epic (container pickers). Uses DetailSection with
blue accent, always open.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `ExecutionContext` component (Tier 2)

**Purpose:** Collapsible section for system_prompt, tools, files, environment, max_duration_ms, token_budget.

**Files:**
- Create: `apps/gui/src/components/domain/execution-context.tsx`

- [ ] **Step 6.1: Create `execution-context.tsx`**

```tsx
import { useMemo } from 'react'
import { Trash2, Plus } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import { formatDurationMs, formatTokenBudget } from '@/lib/sentinel-display'
import { UNLIMITED, type Task } from '@/lib/types'

interface ExecutionContextProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function ExecutionContext({ task, editing, draft, onDraftChange }: ExecutionContextProps) {
  const source = editing ? draft : task

  const summary = useMemo(() => {
    const parts: string[] = []
    if (source.tools.length > 0) parts.push(`${source.tools.length} tools`)
    if (source.files.length > 0) parts.push(`${source.files.length} files`)
    const envCount = Object.keys(source.environment).length
    if (envCount > 0) parts.push(`${envCount} env`)
    return parts.join(' · ') || 'empty'
  }, [source])

  return (
    <DetailSection label="Execution Context" accent="violet" summary={summary}>
      <div className="flex flex-col gap-4">
        {/* system_prompt */}
        <FieldRow label="System Prompt">
          {editing ? (
            <Textarea
              value={source.system_prompt}
              onChange={(e) => onDraftChange('system_prompt', e.target.value)}
              rows={4}
              className="text-[13px]"
            />
          ) : source.system_prompt ? (
            <pre className="whitespace-pre-wrap text-[13px] text-zinc-300 font-sans">
              {source.system_prompt}
            </pre>
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* tools */}
        <FieldRow label="Tools">
          {editing ? (
            <StringListInput
              value={source.tools}
              onChange={(v) => onDraftChange('tools', v)}
              placeholder="comma-separated (e.g. bash, git, grep)"
            />
          ) : source.tools.length > 0 ? (
            <ChipList items={source.tools} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* files */}
        <FieldRow label="Files">
          {editing ? (
            <StringListInput
              value={source.files}
              onChange={(v) => onDraftChange('files', v)}
              placeholder="comma-separated file paths"
            />
          ) : source.files.length > 0 ? (
            <ChipList items={source.files} mono />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* environment */}
        <FieldRow label="Environment">
          {editing ? (
            <EnvEditor
              value={source.environment}
              onChange={(v) => onDraftChange('environment', v)}
            />
          ) : Object.keys(source.environment).length > 0 ? (
            <EnvTable value={source.environment} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        {/* max_duration_ms */}
        <FieldRow label="Max Duration">
          {editing ? (
            <NullableSentinelInput
              value={source.max_duration_ms}
              onChange={(v) => onDraftChange('max_duration_ms', v)}
            />
          ) : (
            <SentinelText display={formatDurationMs(source.max_duration_ms)} />
          )}
        </FieldRow>

        {/* token_budget */}
        <FieldRow label="Token Budget">
          {editing ? (
            <NullableSentinelInput
              value={source.token_budget}
              onChange={(v) => onDraftChange('token_budget', v)}
            />
          ) : (
            <SentinelText display={formatTokenBudget(source.token_budget)} />
          )}
        </FieldRow>
      </div>
    </DetailSection>
  )
}

/* ----- local primitives ----- */

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-4 items-start">
      <div className="text-[10px] uppercase tracking-[.18em] text-zinc-600 pt-1">{label}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function Empty() {
  return <span className="text-[13px] italic text-zinc-600">—</span>
}

function ChipList({ items, mono }: { items: string[]; mono?: boolean }) {
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item, i) => (
        <span
          key={`${item}-${i}`}
          className={`px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300 ${mono ? 'font-mono' : ''}`}
        >
          {item}
        </span>
      ))}
    </div>
  )
}

function StringListInput({
  value,
  onChange,
  placeholder,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder: string
}) {
  const textValue = value.join(', ')
  return (
    <Input
      value={textValue}
      onChange={(e) => {
        const items = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        onChange(items)
      }}
      placeholder={placeholder}
      className="h-7 text-[13px]"
    />
  )
}

function EnvTable({ value }: { value: Record<string, string> }) {
  const entries = Object.entries(value)
  return (
    <div className="flex flex-col gap-1">
      {entries.map(([k, v]) => (
        <div key={k} className="grid grid-cols-[140px_1fr] gap-2 text-[12px] font-mono">
          <span className="text-zinc-500 truncate">{k}</span>
          <span className="text-zinc-300 truncate">{v}</span>
        </div>
      ))}
    </div>
  )
}

function EnvEditor({
  value,
  onChange,
}: {
  value: Record<string, string>
  onChange: (v: Record<string, string>) => void
}) {
  const entries = Object.entries(value)

  function update(idx: number, key: string, val: string) {
    const next: Record<string, string> = {}
    entries.forEach(([k, v], i) => {
      if (i === idx) {
        if (key !== '') next[key] = val
      } else {
        next[k] = v
      }
    })
    onChange(next)
  }

  function addRow() {
    onChange({ ...value, '': '' })
  }

  function removeRow(idx: number) {
    const next: Record<string, string> = {}
    entries.forEach(([k, v], i) => {
      if (i !== idx) next[k] = v
    })
    onChange(next)
  }

  return (
    <div className="flex flex-col gap-1">
      {entries.map(([k, v], idx) => (
        <div key={idx} className="grid grid-cols-[1fr_1fr_auto] gap-2">
          <Input
            value={k}
            onChange={(e) => update(idx, e.target.value, v)}
            placeholder="KEY"
            className="h-7 text-[12px] font-mono"
          />
          <Input
            value={v}
            onChange={(e) => update(idx, k, e.target.value)}
            placeholder="value"
            className="h-7 text-[12px] font-mono"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => removeRow(idx)}
            className="h-7 w-7 p-0"
            aria-label={`Remove ${k || 'row'}`}
          >
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={addRow}
        className="h-7 text-[11px] uppercase tracking-[.18em] self-start"
      >
        <Plus className="h-3 w-3 mr-1" /> Add
      </Button>
    </div>
  )
}

function SentinelText({ display }: { display: ReturnType<typeof formatDurationMs> }) {
  const cls =
    display.mode === 'default'
      ? 'italic text-zinc-600'
      : display.mode === 'unlimited'
        ? 'font-mono text-zinc-400'
        : 'text-zinc-300'
  return <span className={`text-[13px] ${cls}`}>{display.label}</span>
}

function NullableSentinelInput({
  value,
  onChange,
}: {
  value: number | null
  onChange: (v: number | null) => void
}) {
  const mode: 'default' | 'unlimited' | 'value' =
    value === null ? 'default' : value === UNLIMITED ? 'unlimited' : 'value'
  return (
    <div className="flex items-center gap-2">
      <Select
        value={mode}
        onValueChange={(m) => {
          if (m === 'default') onChange(null)
          else if (m === 'unlimited') onChange(UNLIMITED)
          else onChange(1)
        }}
      >
        <SelectTrigger className="h-7 w-28 text-[12px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="default">default</SelectItem>
          <SelectItem value="unlimited">unlimited</SelectItem>
          <SelectItem value="value">value</SelectItem>
        </SelectContent>
      </Select>
      {mode === 'value' && (
        <Input
          type="number"
          min={0}
          value={value ?? 0}
          onChange={(e) => onChange(Number(e.target.value))}
          className="h-7 w-28 text-[13px]"
        />
      )}
    </div>
  )
}
```

- [ ] **Step 6.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds.

- [ ] **Step 6.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 6.4: Commit**

```bash
git add apps/gui/src/components/domain/execution-context.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add ExecutionContext Tier 2 section component

Collapsible violet-accent section for system_prompt, tools, files,
environment (key/value editor), max_duration_ms, token_budget
(sentinel-aware). Summary shows field counts when collapsed.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: `LifecycleRules` component (Tier 3)

**Purpose:** Collapsible section for the four lifecycle enum fields plus escalation_chain, quality_gates, blocked_reason.

**Files:**
- Create: `apps/gui/src/components/domain/lifecycle-rules.tsx`

- [ ] **Step 7.1: Create `lifecycle-rules.tsx`**

```tsx
import { useMemo } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import type { Task, OnDone, OnFail, OnReview, OnDoneMerge } from '@/lib/types'

const ON_DONE_OPTIONS: OnDone[] = ['close', 'review', 'notify']
const ON_FAIL_OPTIONS: OnFail[] = ['retry', 'block', 'escalate', 'notify']
const ON_REVIEW_OPTIONS: OnReview[] = ['pause', 'notify', 'auto-approve']
const ON_DONE_MERGE_OPTIONS: OnDoneMerge[] = ['none', 'auto', 'pr', 'auto-resolve']

interface LifecycleRulesProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function LifecycleRules({ task, editing, draft, onDraftChange }: LifecycleRulesProps) {
  const source = editing ? draft : task

  const summary = useMemo(
    () => [source.on_done, source.on_fail, source.on_review, source.on_done_merge].join(' · '),
    [source.on_done, source.on_fail, source.on_review, source.on_done_merge],
  )

  return (
    <DetailSection label="Lifecycle Rules" accent="amber" summary={summary}>
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-x-4 gap-y-3">
          <EnumField
            label="On Done"
            editing={editing}
            value={source.on_done}
            options={ON_DONE_OPTIONS}
            onChange={(v) => onDraftChange('on_done', v as OnDone)}
          />
          <EnumField
            label="On Fail"
            editing={editing}
            value={source.on_fail}
            options={ON_FAIL_OPTIONS}
            onChange={(v) => onDraftChange('on_fail', v as OnFail)}
          />
          <EnumField
            label="On Review"
            editing={editing}
            value={source.on_review}
            options={ON_REVIEW_OPTIONS}
            onChange={(v) => onDraftChange('on_review', v as OnReview)}
          />
          <EnumField
            label="On Done Merge"
            editing={editing}
            value={source.on_done_merge}
            options={ON_DONE_MERGE_OPTIONS}
            onChange={(v) => onDraftChange('on_done_merge', v as OnDoneMerge)}
          />
        </div>

        <FieldRow label="Escalation Chain">
          {editing ? (
            <StringListInput
              value={source.escalation_chain}
              onChange={(v) => onDraftChange('escalation_chain', v)}
              placeholder="comma-separated agent names"
            />
          ) : source.escalation_chain.length > 0 ? (
            <ChipList items={source.escalation_chain} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Quality Gates">
          {editing ? (
            <StringListInput
              value={source.quality_gates}
              onChange={(v) => onDraftChange('quality_gates', v)}
              placeholder="comma-separated gate names"
            />
          ) : source.quality_gates.length > 0 ? (
            <ChipList items={source.quality_gates} />
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Blocked Reason">
          {editing ? (
            <Textarea
              value={source.blocked_reason}
              onChange={(e) => onDraftChange('blocked_reason', e.target.value)}
              rows={2}
              className="text-[13px]"
            />
          ) : source.blocked_reason ? (
            <span className="text-[13px] text-zinc-300 whitespace-pre-wrap">
              {source.blocked_reason}
            </span>
          ) : (
            <Empty />
          )}
        </FieldRow>
      </div>
    </DetailSection>
  )
}

/* ----- local primitives ----- */

function EnumField({
  label,
  editing,
  value,
  options,
  onChange,
}: {
  label: string
  editing: boolean
  value: string
  options: readonly string[]
  onChange: (v: string) => void
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[9px] uppercase tracking-[.18em] text-zinc-600">{label}</span>
      {editing ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger className="h-7 text-[12px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <span className="inline-flex w-fit px-2 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300 uppercase tracking-[.08em]">
          {value}
        </span>
      )}
    </div>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-4 items-start">
      <div className="text-[10px] uppercase tracking-[.18em] text-zinc-600 pt-1">{label}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function Empty() {
  return <span className="text-[13px] italic text-zinc-600">—</span>
}

function ChipList({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item, i) => (
        <span
          key={`${item}-${i}`}
          className="px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] text-zinc-300"
        >
          {item}
        </span>
      ))}
    </div>
  )
}

function StringListInput({
  value,
  onChange,
  placeholder,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder: string
}) {
  return (
    <Input
      value={value.join(', ')}
      onChange={(e) => {
        const items = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        onChange(items)
      }}
      placeholder={placeholder}
      className="h-7 text-[13px]"
    />
  )
}
```

- [ ] **Step 7.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds.

- [ ] **Step 7.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 7.4: Commit**

```bash
git add apps/gui/src/components/domain/lifecycle-rules.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add LifecycleRules Tier 3 section component

Collapsible amber-accent section with four enum selects
(on_done, on_fail, on_review, on_done_merge) plus
escalation_chain, quality_gates, blocked_reason. Collapsed
summary joins the four enum values.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: `DeliverablesAndDeps` component (Tier 4)

**Purpose:** Collapsible section for depends_on, deliverable_preset, and the deliverables array.

**Files:**
- Create: `apps/gui/src/components/domain/deliverables-deps.tsx`

- [ ] **Step 8.1: Create `deliverables-deps.tsx`**

```tsx
import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { Trash2, Plus } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DetailSection } from './detail-section'
import type { Task, Deliverable, DeliverableType } from '@/lib/types'

const DELIVERABLE_TYPES: DeliverableType[] = [
  'diff',
  'test-results',
  'screenshot',
  'pr-link',
  'branch',
  'commit',
  'log',
  'finding',
  'report',
  'note',
  'metrics',
  'custom',
]

interface DeliverablesAndDepsProps {
  task: Task
  editing: boolean
  draft: Task
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
}

export function DeliverablesAndDeps({
  task,
  editing,
  draft,
  onDraftChange,
}: DeliverablesAndDepsProps) {
  const source = editing ? draft : task

  const summary = useMemo(() => {
    const parts: string[] = []
    if (source.deliverables.length > 0) parts.push(`${source.deliverables.length} deliverables`)
    if (source.depends_on.length > 0) parts.push(`${source.depends_on.length} deps`)
    return parts.join(' · ') || 'empty'
  }, [source.deliverables.length, source.depends_on.length])

  return (
    <DetailSection label="Deliverables & Dependencies" accent="red" summary={summary}>
      <div className="flex flex-col gap-4">
        <FieldRow label="Depends On">
          {editing ? (
            <StringListInput
              value={source.depends_on}
              onChange={(v) => onDraftChange('depends_on', v)}
              placeholder="comma-separated task IDs (e.g. tsk_abc, tsk_xyz)"
              mono
            />
          ) : source.depends_on.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {source.depends_on.map((id) => (
                <Link
                  key={id}
                  to={`/tasks/${id}`}
                  className="px-1.5 py-0.5 rounded border border-zinc-800 bg-zinc-900 text-[11px] font-mono text-blue-400 hover:text-blue-300"
                >
                  {id}
                </Link>
              ))}
            </div>
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Deliverable Preset">
          {editing ? (
            <Input
              value={source.deliverable_preset}
              onChange={(e) => onDraftChange('deliverable_preset', e.target.value)}
              placeholder="preset name"
              className="h-7 text-[13px]"
            />
          ) : source.deliverable_preset ? (
            <span className="text-[13px] text-zinc-300">{source.deliverable_preset}</span>
          ) : (
            <Empty />
          )}
        </FieldRow>

        <FieldRow label="Deliverables">
          {editing ? (
            <DeliverablesEditor
              value={source.deliverables}
              onChange={(v) => onDraftChange('deliverables', v)}
            />
          ) : source.deliverables.length > 0 ? (
            <DeliverablesTable value={source.deliverables} />
          ) : (
            <Empty />
          )}
        </FieldRow>
      </div>
    </DetailSection>
  )
}

/* ----- local primitives ----- */

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-4 items-start">
      <div className="text-[10px] uppercase tracking-[.18em] text-zinc-600 pt-1">{label}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function Empty() {
  return <span className="text-[13px] italic text-zinc-600">—</span>
}

function StringListInput({
  value,
  onChange,
  placeholder,
  mono,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder: string
  mono?: boolean
}) {
  return (
    <Input
      value={value.join(', ')}
      onChange={(e) => {
        const items = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        onChange(items)
      }}
      placeholder={placeholder}
      className={`h-7 text-[13px] ${mono ? 'font-mono' : ''}`}
    />
  )
}

function DeliverablesTable({ value }: { value: Deliverable[] }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="grid grid-cols-[120px_80px_1fr] gap-2 text-[9px] uppercase tracking-[.18em] text-zinc-600 pb-1 border-b border-zinc-800/50">
        <span>Type</span>
        <span>Required</span>
        <span>Description</span>
      </div>
      {value.map((d, i) => (
        <div key={i} className="grid grid-cols-[120px_80px_1fr] gap-2 text-[12px]">
          <span className="text-zinc-300">{d.type}</span>
          <span className="text-zinc-400">{d.required ? 'yes' : 'no'}</span>
          <span className="text-zinc-400 truncate">{d.description ?? '—'}</span>
        </div>
      ))}
    </div>
  )
}

function DeliverablesEditor({
  value,
  onChange,
}: {
  value: Deliverable[]
  onChange: (v: Deliverable[]) => void
}) {
  function updateRow(idx: number, patch: Partial<Deliverable>) {
    onChange(value.map((d, i) => (i === idx ? { ...d, ...patch } : d)))
  }
  function removeRow(idx: number) {
    onChange(value.filter((_, i) => i !== idx))
  }
  function addRow() {
    onChange([...value, { type: 'diff', required: false, description: '' }])
  }

  return (
    <div className="flex flex-col gap-2">
      {value.map((d, i) => (
        <div key={i} className="grid grid-cols-[140px_auto_1fr_auto] gap-2 items-center">
          <Select
            value={d.type}
            onValueChange={(v) => updateRow(i, { type: v as DeliverableType })}
          >
            <SelectTrigger className="h-7 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {DELIVERABLE_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Switch
            checked={d.required}
            onCheckedChange={(v) => updateRow(i, { required: v })}
            aria-label="Required"
          />
          <Input
            value={d.description ?? ''}
            onChange={(e) => updateRow(i, { description: e.target.value })}
            placeholder="description"
            className="h-7 text-[12px]"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => removeRow(i)}
            className="h-7 w-7 p-0"
            aria-label="Remove deliverable"
          >
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={addRow}
        className="h-7 text-[11px] uppercase tracking-[.18em] self-start"
      >
        <Plus className="h-3 w-3 mr-1" /> Add Deliverable
      </Button>
    </div>
  )
}
```

- [ ] **Step 8.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds.

- [ ] **Step 8.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 8.4: Commit**

```bash
git add apps/gui/src/components/domain/deliverables-deps.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add DeliverablesAndDeps Tier 4 section component

Collapsible red-accent section for depends_on (task ID chips
linking to detail pages), deliverable_preset, and the
deliverables array (editable type/required/description rows).

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: `TaskDetailHeader` component

**Purpose:** HUD-style header replacing the old `DetailHeader` usage on the task detail page. Renders breadcrumb, title, badges, and either transition buttons (view mode) or Save/Cancel (edit mode).

**Files:**
- Create: `apps/gui/src/components/domain/task-detail-header.tsx`

- [ ] **Step 9.1: Create `task-detail-header.tsx`**

```tsx
import { Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { StatusBadge } from './status-badge'
import { PriorityBadge } from './priority-badge'
import { TagChip } from './tag-chip'
import { TASK_STATUSES, PRIORITIES } from '@/lib/constants'
import type { Task, TaskStatus, Tag } from '@/lib/types'

const TRANSITION_LABELS: Partial<Record<TaskStatus, string>> = {
  todo: 'Mark To Do',
  queued: 'Queue',
  doing: 'Start',
  review: 'Send for Review',
  done: 'Mark Done',
  blocked: 'Block',
  paused: 'Pause',
  archived: 'Archive',
}

interface TaskDetailHeaderProps {
  task: Task
  editing: boolean
  draft: Task
  saving: boolean
  onDraftChange: <K extends keyof Task>(field: K, value: Task[K]) => void
  onTransition: (status: TaskStatus) => void
  onEdit: () => void
  onSave: () => void
  onCancel: () => void
}

export function TaskDetailHeader({
  task,
  editing,
  draft,
  saving,
  onDraftChange,
  onTransition,
  onEdit,
  onSave,
  onCancel,
}: TaskDetailHeaderProps) {
  const nextStatuses = TASK_STATUSES.filter((s) => s !== task.status).slice(0, 4)
  const source = editing ? draft : task

  return (
    <div className="border-b border-zinc-800/80 bg-zinc-950 px-4 py-3">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 mb-2">
        <Link
          to="/"
          className="flex items-center gap-1 text-[10px] uppercase tracking-[.18em] text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          <ArrowLeft className="h-3 w-3" />
          Board
        </Link>
        <span className="text-zinc-700 text-[10px]">/</span>
        <span className="text-[10px] text-zinc-500 font-mono">{task.id}</span>
      </div>

      {/* Title + badges + actions row */}
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-2 min-w-0 flex-1">
          {editing ? (
            <Input
              value={source.title}
              onChange={(e) => onDraftChange('title', e.target.value)}
              placeholder="Task title"
              className="h-8 text-base font-semibold tracking-[.02em] bg-zinc-900 border-zinc-800"
            />
          ) : (
            <h1 className="text-base font-semibold tracking-[.02em] text-zinc-100 leading-tight">
              {task.title}
            </h1>
          )}

          <div className="flex items-center gap-2 flex-wrap">
            <StatusBadge status={task.status} />
            {editing ? (
              <Select
                value={String(source.priority)}
                onValueChange={(v) => onDraftChange('priority', Number(v))}
              >
                <SelectTrigger className="h-6 w-20 text-[11px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PRIORITIES.map((p) => (
                    <SelectItem key={p.value} value={String(p.value)}>
                      {p.label} ({p.description})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <PriorityBadge priority={task.priority} />
            )}
            {editing ? (
              <TagsInput
                value={source.tags}
                onChange={(v) => onDraftChange('tags', v)}
              />
            ) : (
              task.tags.map((tag) => <TagChip key={tag.slug} tag={tag} />)
            )}
          </div>
        </div>

        {/* Action buttons */}
        <div className="flex flex-wrap gap-1.5 shrink-0">
          {editing ? (
            <>
              <Button
                size="sm"
                onClick={onSave}
                disabled={saving || !source.title.trim()}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                {saving ? 'Saving...' : 'Save'}
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={onCancel}
                disabled={saving}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                Cancel
              </Button>
            </>
          ) : (
            <>
              {nextStatuses.map((s) => (
                <Button
                  key={s}
                  variant="outline"
                  size="sm"
                  className="text-[11px] h-7 uppercase tracking-[.18em]"
                  onClick={() => onTransition(s)}
                >
                  {TRANSITION_LABELS[s] ?? s}
                </Button>
              ))}
              <Button
                size="sm"
                variant="outline"
                onClick={onEdit}
                className="text-[11px] h-7 uppercase tracking-[.18em]"
              >
                Edit
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * Simple comma-separated tag name input. On blur/enter, parses
 * the text into Tag[] stubs. The API auto-resolves/creates tags by name
 * on save, so we only need to track names here.
 */
function TagsInput({
  value,
  onChange,
}: {
  value: Tag[]
  onChange: (v: Tag[]) => void
}) {
  return (
    <Input
      value={value.map((t) => t.name).join(', ')}
      onChange={(e) => {
        const names = e.target.value
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0)
        // Build Tag[] stubs — the diff helper reads names only.
        const tags: Tag[] = names.map((name) => ({
          slug: name.toLowerCase().replace(/\s+/g, '-'),
          name,
          description: '',
          color: 'zinc',
          created_at: '',
          updated_at: '',
        }))
        onChange(tags)
      }}
      placeholder="comma-separated tags"
      className="h-6 w-64 text-[11px]"
    />
  )
}
```

- [ ] **Step 9.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds.

- [ ] **Step 9.3: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 9.4: Commit**

```bash
git add apps/gui/src/components/domain/task-detail-header.tsx
git commit -m "$(cat <<'EOF'
feat(gui): add TaskDetailHeader with HUD-style view/edit modes

Replaces the old DetailHeader for task detail pages. Renders
breadcrumb, title (input in edit), status/priority/tags badges
(selects in edit), and either transition buttons or Save/Cancel.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Rewrite `TaskDetailPage`

**Purpose:** Compose the header and four section components, manage draft state, handle `?edit=1` toggle, save/cancel flow, and container picker loading. Activity tabs stay but get HUD styling and hide in edit mode.

**Files:**
- Modify (full rewrite): `apps/gui/src/pages/TaskDetailPage.tsx`

- [ ] **Step 10.1: Rewrite `TaskDetailPage.tsx`**

Replace the entire contents of `apps/gui/src/pages/TaskDetailPage.tsx` with:

```tsx
import { useState, useEffect, useCallback } from 'react'
import { useParams, useSearchParams, Link } from 'react-router-dom'
import { FolderOpen } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { CommentList } from '@/components/domain/comment-list'
import { RunCard } from '@/components/domain/run-card'
import { EmptyState } from '@/components/domain/empty-state'
import { TaskDetailHeader } from '@/components/domain/task-detail-header'
import { DetailSection } from '@/components/domain/detail-section'
import { TaskProperties } from '@/components/domain/task-properties'
import { ExecutionContext } from '@/components/domain/execution-context'
import { LifecycleRules } from '@/components/domain/lifecycle-rules'
import { DeliverablesAndDeps } from '@/components/domain/deliverables-deps'
import { useApi } from '@/hooks/use-api'
import { computeTaskDiff } from '@/lib/task-diff'
import type {
  Task,
  Run,
  Comment,
  Artifact,
  TaskStatus,
  Project,
  Sprint,
  Epic,
} from '@/lib/types'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const [searchParams, setSearchParams] = useSearchParams()
  const editing = searchParams.get('edit') === '1'

  const [task, setTask] = useState<Task | null>(null)
  const [draft, setDraft] = useState<Task | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  // Tab data — lazy loaded
  const [comments, setComments] = useState<Comment[] | null>(null)
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null)
  const [activeTab, setActiveTab] = useState('comments')

  // Container picker options (loaded when edit mode activates)
  const [projects, setProjects] = useState<Project[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [pickersLoading, setPickersLoading] = useState(false)

  // Fetch task on mount / id change
  useEffect(() => {
    if (!id) return
    setLoading(true)
    api
      .getTask(id)
      .then((t) => {
        setTask(t)
        setError(null)
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  // Initialize/reset draft when editing starts, or clear when it ends
  useEffect(() => {
    if (editing && task) {
      setDraft((prev) => prev ?? { ...task })
    } else {
      setDraft(null)
      setSaveError(null)
    }
  }, [editing, task])

  // Load container picker options when edit mode activates
  useEffect(() => {
    if (!editing) return
    setPickersLoading(true)
    Promise.allSettled([
      api.listProjects(),
      api.listSprints(),
      api.listEpics(),
    ]).then(([pRes, sRes, eRes]) => {
      if (pRes.status === 'fulfilled') setProjects(pRes.value.projects)
      if (sRes.status === 'fulfilled') setSprints(sRes.value.sprints)
      if (eRes.status === 'fulfilled') setEpics(eRes.value.epics)
      setPickersLoading(false)
    })
  }, [editing, api])

  // Lazy-load tab content (view mode only)
  useEffect(() => {
    if (!id || !task || editing) return
    if (activeTab === 'comments' && comments === null) {
      api.listComments(id).then(setComments).catch(() => setComments([]))
    }
    if (activeTab === 'runs' && runs === null) {
      api.listRuns(id).then(setRuns).catch(() => setRuns([]))
    }
    if (activeTab === 'artifacts' && artifacts === null) {
      api.listArtifacts(id).then(setArtifacts).catch(() => setArtifacts([]))
    }
  }, [activeTab, id, task, editing, comments, runs, artifacts, api])

  const updateDraft = useCallback(
    <K extends keyof Task>(field: K, value: Task[K]) => {
      setDraft((prev) => (prev ? { ...prev, [field]: value } : prev))
    },
    [],
  )

  async function handleAddComment(content: string) {
    if (!id) return
    const comment = await api.addComment(id, content)
    setComments((prev) => [...(prev ?? []), comment])
  }

  async function handleTransition(status: TaskStatus) {
    if (!id) return
    try {
      const updated = await api.transitionTask(id, status)
      setTask(updated)
    } catch {
      // no-op — the badge doesn't change, user sees no update
    }
  }

  function handleEdit() {
    setSearchParams({ edit: '1' })
  }

  function handleCancel() {
    setSaveError(null)
    setSearchParams({})
  }

  async function handleSave() {
    if (!id || !task || !draft) return
    setSaving(true)
    setSaveError(null)
    try {
      const diff = computeTaskDiff(task, draft)
      if (Object.keys(diff).length === 0) {
        // Nothing changed — just exit edit mode
        setSearchParams({})
        return
      }
      const updated = await api.updateTask(id, diff)
      setTask(updated)
      setDraft(null)
      setSearchParams({})
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-4 flex flex-col gap-3">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-32 w-full rounded-md" />
        <Skeleton className="h-16 w-full rounded-md" />
        <Skeleton className="h-16 w-full rounded-md" />
      </div>
    )
  }

  if (error || !task) {
    return (
      <div className="p-4">
        <EmptyState variant="error" description={error ?? 'Task not found.'} />
      </div>
    )
  }

  // In edit mode, draft should be set — fall back to task to keep TS happy
  const displayDraft = draft ?? task

  return (
    <div className="flex h-full flex-col">
      <TaskDetailHeader
        task={task}
        editing={editing}
        draft={displayDraft}
        saving={saving}
        onDraftChange={updateDraft}
        onTransition={handleTransition}
        onEdit={handleEdit}
        onSave={handleSave}
        onCancel={handleCancel}
      />

      {/* Save error banner */}
      {saveError && (
        <div className="border-b border-red-900/50 bg-red-950/40 px-4 py-2">
          <span className="text-[12px] text-red-300">{saveError}</span>
        </div>
      )}

      {/* Field sections */}
      <div className="flex-1 overflow-auto">
        <div className="flex flex-col gap-3 p-4">
          <TaskProperties
            task={task}
            editing={editing}
            draft={displayDraft}
            onDraftChange={updateDraft}
            projects={projects}
            sprints={sprints}
            epics={epics}
            pickersLoading={pickersLoading}
          />

          {/* Description — inline, zinc accent, always open */}
          <DetailSection label="Description" accent="zinc" collapsible={false}>
            {editing ? (
              <Textarea
                value={displayDraft.description}
                onChange={(e) => updateDraft('description', e.target.value)}
                rows={4}
                className="text-[13px]"
                placeholder="Task description"
              />
            ) : task.description ? (
              <p className="text-[13px] text-zinc-300 whitespace-pre-wrap">{task.description}</p>
            ) : (
              <span className="text-[13px] italic text-zinc-600">—</span>
            )}
          </DetailSection>

          <ExecutionContext
            task={task}
            editing={editing}
            draft={displayDraft}
            onDraftChange={updateDraft}
          />

          <LifecycleRules
            task={task}
            editing={editing}
            draft={displayDraft}
            onDraftChange={updateDraft}
          />

          <DeliverablesAndDeps
            task={task}
            editing={editing}
            draft={displayDraft}
            onDraftChange={updateDraft}
          />
        </div>

        {/* Activity zone — hidden in edit mode */}
        {!editing && (
          <div className="border-t border-zinc-800/80 px-4 py-3">
            <Tabs value={activeTab} onValueChange={setActiveTab}>
              <TabsList className="bg-transparent border-b border-zinc-800/50 rounded-none p-0 h-auto mb-3">
                <TabsTrigger
                  value="comments"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Comments
                </TabsTrigger>
                <TabsTrigger
                  value="runs"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Runs
                </TabsTrigger>
                <TabsTrigger
                  value="artifacts"
                  className="text-[10px] uppercase tracking-[.18em] data-[state=active]:border-b data-[state=active]:border-zinc-100 data-[state=active]:text-zinc-100 text-zinc-500 rounded-none bg-transparent px-3 py-1.5"
                >
                  Artifacts
                </TabsTrigger>
              </TabsList>

              <TabsContent value="comments">
                <CommentList
                  comments={comments ?? []}
                  loading={comments === null}
                  onAddComment={handleAddComment}
                />
              </TabsContent>

              <TabsContent value="runs">
                {runs === null ? (
                  <div className="flex flex-col gap-3">
                    {Array.from({ length: 2 }).map((_, i) => (
                      <Skeleton key={i} className="h-24 w-full rounded-md" />
                    ))}
                  </div>
                ) : runs.length === 0 ? (
                  <EmptyState
                    variant="no-results"
                    title="No runs yet"
                    description="This task hasn't been executed yet."
                  />
                ) : (
                  <div className="flex flex-col gap-3">
                    {runs.map((run) => (
                      <RunCard key={run.id} run={run} />
                    ))}
                  </div>
                )}
              </TabsContent>

              <TabsContent value="artifacts">
                {artifacts === null ? (
                  <Skeleton className="h-24 w-full rounded-md" />
                ) : artifacts.length === 0 ? (
                  <EmptyState
                    variant="no-results"
                    title="No artifacts"
                    description="No artifacts have been produced for this task."
                  />
                ) : (
                  <div className="flex flex-col gap-3">
                    {artifacts.map((artifact) => (
                      <div
                        key={artifact.id}
                        className="rounded-md border border-zinc-800/50 p-3"
                      >
                        <div className="flex items-center gap-2 mb-1">
                          <FolderOpen className="h-3 w-3 text-zinc-500" />
                          <span className="text-[10px] uppercase tracking-[.18em] text-zinc-500">
                            {artifact.type}
                          </span>
                        </div>
                        {artifact.file_path && (
                          <p className="text-[11px] font-mono text-zinc-400 break-all">
                            {artifact.file_path}
                          </p>
                        )}
                        {artifact.url && (
                          <Link
                            to={artifact.url}
                            className="text-[11px] text-blue-400 hover:text-blue-300 break-all"
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {artifact.url}
                          </Link>
                        )}
                        {artifact.content && (
                          <pre className="mt-2 text-[11px] bg-zinc-900/60 rounded p-2 overflow-auto max-h-40 whitespace-pre-wrap text-zinc-300">
                            {artifact.content}
                          </pre>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>
            </Tabs>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 10.2: Verify it builds**

Working directory: `apps/gui/`

Run: `npm run build`
Expected: build succeeds. This is the integration task — all component type contracts get checked against each other here.

If it fails, the most likely causes are:
- Prop name or type mismatch between the page and one of the section components (check against the component file)
- Missing import
- TS strict checks on optional chaining (e.g., `draft!` where the helper expects `Task`)

Fix any errors before proceeding.

- [ ] **Step 10.3: Run unit tests**

Working directory: `apps/gui/`

Run: `npm run test:run`
Expected: All tests from Tasks 2 and 3 still pass (26 total — 9 task-diff + 17 sentinel-display).

- [ ] **Step 10.4: Lint**

Working directory: `apps/gui/`

Run: `npm run lint`
Expected: no new errors.

- [ ] **Step 10.5: Commit**

```bash
git add apps/gui/src/pages/TaskDetailPage.tsx
git commit -m "$(cat <<'EOF'
feat(gui): rewrite TaskDetailPage to HUD language with ?edit=1 mode

Replaces the shadcn Card-based detail page with a HUD-styled
full-width section layout. Composes TaskDetailHeader and the
four Tier section components, manages draft state, ?edit=1
toggle, save/cancel flow, save error banner, and lazy-loads
container picker options when edit mode activates. Activity
tabs restyled and hidden in edit mode.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Manual QA + final verification

**Purpose:** End-to-end verification that everything works together. No code changes unless bugs are found.

**Files:** None (manual testing)

- [ ] **Step 11.1: Start the dev stack**

- Confirm clockwork-api is running (via cerberus). Default port 8990.
- Confirm clockwork-frontend is running (via cerberus). Default port 5175.

If either is down:

```bash
# Check status
cerberus status

# Start if needed
cerberus start clockwork-api
cerberus start clockwork-frontend
```

- [ ] **Step 11.2: Load the board and navigate to a task**

Open `http://localhost:5175/`. Verify the board loads (tasks visible). Click a task row to navigate to `/tasks/:id`.

**Expected view-mode checklist:**
- Header: `← Board / tsk_xxx` breadcrumb, title, status/priority/tags badges, transition buttons, Edit button
- Properties section (blue accent bar, always open): 3×3 grid, linked project/sprint/epic if set
- Description section (zinc accent, always open): plain text or "—"
- Execution Context (violet, collapsed): shows summary like "3 tools · 2 files"
- Lifecycle Rules (amber, collapsed): shows enum summary like "close · retry · pause · none"
- Deliverables & Dependencies (red, collapsed): shows count summary
- Below field sections: tabs row (Comments/Runs/Artifacts) in HUD styling
- Clicking each tab lazy-loads its content

- [ ] **Step 11.3: Test section collapse**

Click the header of each collapsible section (Execution Context, Lifecycle Rules, Deliverables). Verify:
- ChevronRight rotates 90° when open
- Fields appear below a separator
- Summary text hides when open
- Clicking again collapses it back

- [ ] **Step 11.4: Enter edit mode**

Click the "Edit" button in the header. URL should change to `/tasks/:id?edit=1`.

**Expected edit-mode checklist:**
- Title becomes an input (pre-filled)
- Priority badge becomes a select
- Tags become a comma-separated input
- Transition buttons disappear; Save + Cancel appear
- Activity tabs at the bottom are hidden
- Property fields become inputs (executor, agent_profile, etc.)
- Container pickers (project/sprint/epic) show "loading..." briefly then populate
- Cost Budget shows a mode select (default/unlimited/value) and value input when `value` is chosen
- Section sections are still collapsible
- Save button is disabled if title is empty

- [ ] **Step 11.5: Make a trivial edit and save**

- Change the title to something like `{original-title} (edited)`
- Click Save
- Verify: `?edit=1` is dropped from URL, view mode returns, title shows the new value, transition buttons reappear
- Refresh the page — the change persists (confirmation that the PATCH went through)
- Revert by clicking Edit again, restoring the original title, Save

- [ ] **Step 11.6: Test cancel**

- Click Edit
- Make a change (e.g., change description)
- Click Cancel
- Verify: `?edit=1` drops, view mode returns, description shows the original value (draft discarded)

- [ ] **Step 11.7: Test save error handling**

Find a way to trigger a validation error. Easiest: edit a task's `depends_on` field in edit mode to reference a nonexistent task ID (e.g., `tsk_nonexistent_xyz`), then Save.

**Expected:**
- Red error banner appears below the header with the API's error message
- Edit mode does NOT exit — draft is preserved
- User can clear the bad dependency and retry

- [ ] **Step 11.8: Test container picker**

- Edit a task
- Change the Project picker from "none" to a real project (or vice versa)
- Save
- Verify: the view shows the new project as a linked name in the Properties section, clicking it navigates to `/projects/:id`

- [ ] **Step 11.9: Verify build, lint, and tests one final time**

Working directory: `apps/gui/`

```bash
npm run test:run
npm run lint
npm run build
```

Expected: all three commands exit 0.

- [ ] **Step 11.10: Commit (if any fixes were needed)**

If Step 11.2–11.8 surfaced bugs and you made fixes, commit them:

```bash
git add apps/gui/src/
git commit -m "$(cat <<'EOF'
fix(gui): address task detail rebuild QA findings

[describe the specific fixes]

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

If there were no bugs, no commit needed — the rebuild is done.

---

## Verification summary

After Task 11 passes, this project is complete. You should have:

- `npm run test:run` passing all 26 tests (9 task-diff + 17 sentinel-display)
- `npm run build` succeeding
- `npm run lint` clean
- Task detail page rendered in HUD language with structured panels
- `?edit=1` whole-page edit mode working for all Tier 1–4 fields
- Activity tabs (Comments/Runs/Artifacts) restyled and hidden in edit mode
- Old `DetailHeader` still used by Epic/Sprint/Project pages (untouched)
- Spec deferred items remain deferred: NavArrows, context-aware back-links, Tier 5 JSON editors, inline edit conveniences, SSE live updates, other detail page rebuilds
