# Tasks Filter Bar Redesign + Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add first-class scoped search to the Operations task list, simplify the filter bar (drop Mode preset, cycle-button Manual, typeahead combobox group selectors), and reorganize into a two-row layout — while keeping detail pages (Sprint/Project/Epic) untouched via an additive prop contract.

**Architecture:** Split the existing monolithic `filter-bar.tsx` into a `filter-bar/` directory with four composable primitives (`FilterSearchInput`, `FilterCycleToggle`, `FilterEntityCombobox`, `FilterChipRow`) and a shell (`FilterBar`) that renders single-row or two-row layout based on whether search props are provided. Extend `ops-filters-storage` to persist the new `search` field; rename the legacy `manual: 'all'` to `'both'` for label honesty; drop the `mode` preset. No backend changes — the existing `/tasks?search=` list filter is already wired.

**Tech Stack:** React 19, TypeScript, Vite, Vitest + jsdom + @testing-library/react, Tailwind, shadcn (Popover, Command/cmdk), lucide-react icons. Test env uses vitest with per-file `@vitest-environment jsdom` pragma for interactive components.

**Reference spec:** `docs/superpowers/specs/2026-04-21-tasks-filter-bar-redesign-design.md`.

---

## Conventions

- **Commit message prefix:** `feat(ops):` for user-visible behavior, `refactor(ops):` for code-move/extract with no behavior change, `test(ops):` for test-only commits.
- **No back-compat shims.** Pre-launch project rule — rename cleanly, delete dead code, no aliases or deprecation logs.
- **No emojis** in code, commits, or UI copy.
- **No `--no-verify`**, no hook bypass. If pre-commit hooks fail, fix the underlying issue.
- **TDD discipline:** new primitive components get their test written and failing before the component exists. Migrations get their new-shape test written before the type/parse changes land.
- **Run tests from** `~/Projects-apps/torque/apps/gui` via `npm run test:run` (one-shot vitest). Prefer targeted file runs during iteration: `npm run test:run -- src/path/to.test.ts`.
- **Dev server:** `npm run dev` from `apps/gui` for manual smoke (Task 10). Backend should be running via Cerberus on `:8990`.

## File Structure

### New files

```
apps/gui/src/components/domain/filter-bar/
├── index.ts                         # re-exports FilterBar
├── filter-bar.tsx                   # shell; switches single-row vs two-row; contains inline chip-row composition
├── filter-search-input.tsx          # debounced search input + / shortcut + Esc clear
├── filter-search-input.test.tsx
├── filter-cycle-toggle.tsx          # generic cycle button (used for Manual)
├── filter-cycle-toggle.test.tsx
├── filter-entity-combobox.tsx       # shadcn Popover + Command typeahead combobox
└── filter-entity-combobox.test.tsx
```

Note: the design spec originally suggested a separate `filter-chip-row.tsx`. In this plan the chip-row composition lives as a `const chipRow = (...)` inside `filter-bar.tsx` (Task 8). Rationale: it's only consumed in one place, the primitives (`FilterCycleToggle`, `FilterEntityCombobox`) are the meaningful reusable units, and an extra file for a JSX-only composition adds indirection without benefit.

### Modified files

```
apps/gui/src/lib/ops-filters-storage.ts       # schema v2: drop mode, add search, rename 'all' → 'both'
apps/gui/src/lib/ops-filters-storage.test.ts  # new migration test + updated fixtures
apps/gui/src/lib/task-list-cursor.ts          # drop mode, add search
apps/gui/src/pages/BoardPage.tsx              # drop Mode, add search state, pass through to FilterBar, two-row mode
apps/gui/src/pages/BoardPage.rehydrate.test.tsx  # update fixtures for new schema
```

### Deleted files

```
apps/gui/src/components/domain/filter-bar.tsx   # moved into filter-bar/ directory (Task 2)
```

### Import surface

All four callers (`BoardPage`, `SprintDetailPage`, `ProjectDetailPage`, `EpicDetailPage`) keep their existing `import { FilterBar } from '@/components/domain/filter-bar'` line working via the new `filter-bar/index.ts` re-export.

---

## Task 1: Storage + state-shape migration (coordinated rename)

Rename `ManualFilter` value `'all'` to `'both'`, drop `mode` from all filter-state modules, add `search` field, and fix every call site in one coherent commit. The test harness for `ops-filters-storage` stays green throughout; `tsc -b` is green at commit time.

**Files:**
- Modify: `apps/gui/src/lib/ops-filters-storage.ts`
- Modify: `apps/gui/src/lib/ops-filters-storage.test.ts`
- Modify: `apps/gui/src/lib/task-list-cursor.ts`
- Modify: `apps/gui/src/pages/BoardPage.tsx`
- Modify: `apps/gui/src/pages/BoardPage.rehydrate.test.tsx`
- Modify: `apps/gui/src/components/domain/filter-bar.tsx` (MANUAL_OPTIONS only; full redesign comes later)

- [ ] **Step 1.1: Write the failing migration tests**

Append two new cases to `apps/gui/src/lib/ops-filters-storage.test.ts` (keep the rest of the file intact for now — we'll update the existing fixtures in Step 1.4).

```ts
  it('migrates legacy manual="all" to "both" on read', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
        mode: 'all',
        manual: 'all',
      }),
    )
    expect(readOpsFilters()?.manual).toBe('both')
  })

  it('drops the legacy mode field and defaults search to "" on read', () => {
    localStorage.setItem(
      'torque:ops:filters:v1',
      JSON.stringify({
        statuses: ['todo'],
        priorities: [],
        projectId: null,
        sprintId: null,
        epicId: null,
        tagSlug: null,
        mode: 'executing',
        manual: 'auto',
      }),
    )
    const restored = readOpsFilters()
    expect(restored).toBeTruthy()
    expect((restored as unknown as Record<string, unknown>).mode).toBeUndefined()
    expect(restored?.search).toBe('')
  })
```

- [ ] **Step 1.2: Run the tests — they should fail**

```bash
cd apps/gui
npm run test:run -- src/lib/ops-filters-storage.test.ts
```

Expected: the two new tests fail (`manual` comes back as `'all'`, `search` is undefined), everything else passes.

- [ ] **Step 1.3: Update `ops-filters-storage.ts` with the new schema and migration**

Replace the file contents with:

```ts
import type { TaskStatus } from './types'

const KEY = 'torque:ops:filters:v1'

/**
 * Manual-flag filter tri-state.
 * - `both`   = no filter (default)
 * - `auto`   = manual=false (scheduler-eligible tasks)
 * - `manual` = manual=true (held for review before dispatch)
 */
export type ManualFilter = 'both' | 'auto' | 'manual'

const MANUAL_VALUES: readonly ManualFilter[] = ['both', 'auto', 'manual'] as const

export function parseManualFilter(raw: unknown): ManualFilter {
  if (typeof raw !== 'string') return 'both'
  // Legacy migration: older blobs stored the default as 'all'.
  if (raw === 'all') return 'both'
  return (MANUAL_VALUES as readonly string[]).includes(raw) ? (raw as ManualFilter) : 'both'
}

export interface OpsFilters {
  statuses: TaskStatus[]
  priorities: number[]
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  manual: ManualFilter
  /** Free-text search over title + description. */
  search: string
}

export function saveOpsFilters(filters: OpsFilters): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(filters))
  } catch {
    // localStorage may be unavailable (private mode / quota) — fail quiet
  }
}

export function readOpsFilters(): OpsFilters | null {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return null
    const f = parsed as Record<string, unknown>
    const statuses = Array.isArray(f.statuses)
      ? (f.statuses as unknown[]).filter((s): s is TaskStatus => typeof s === 'string') as TaskStatus[]
      : []
    const priorities = Array.isArray(f.priorities)
      ? (f.priorities as unknown[]).filter((p): p is number => typeof p === 'number')
      : []
    return {
      statuses,
      priorities,
      projectId: typeof f.projectId === 'string' ? f.projectId : null,
      sprintId: typeof f.sprintId === 'string' ? f.sprintId : null,
      epicId: typeof f.epicId === 'string' ? f.epicId : null,
      tagSlug: typeof f.tagSlug === 'string' ? f.tagSlug : null,
      manual: parseManualFilter(f.manual),
      search: typeof f.search === 'string' ? f.search : '',
    }
  } catch {
    return null
  }
}

export function clearOpsFilters(): void {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // ignore
  }
}
```

Key changes from the previous version:
- `ManualFilter`: `'all'` → `'both'`; `parseManualFilter` migrates `'all'` → `'both'` on read and defaults any other value to `'both'`.
- `OpsFilters`: removed `mode`; added `search: string`.
- `readOpsFilters`: no longer reads `mode`; reads `search` with `''` default; no longer parses `f.mode`.

- [ ] **Step 1.4: Update the existing tests to use the new schema**

Edit `apps/gui/src/lib/ops-filters-storage.test.ts`. Replace every fixture that contains `mode: ...` or `manual: 'all'`:

- Remove `mode` entirely from the `OpsFilters` literals in the round-trip, projectId, clear, and sanitize tests.
- Add `search: ''` (or a non-empty test string for the round-trip case) to every `OpsFilters` literal.
- Replace `manual: 'all'` with `manual: 'both'` in the `OpsFilters` literals.
- In the sanitize test (stored value with `mode: 'all'`), drop the expected `mode` key and add `search: ''` to the expected shape.
- Update the "defaults manual..." test so its stored blob omits the `manual` field entirely (the field name changed semantically from `'all'`→`'both'`); the assertion becomes `expect(readOpsFilters()?.manual).toBe('both')`.
- Round-trip test now has 8 filter fields, not mode+7. Rename the test title to "round-trips all eight filter fields" → "round-trips all eight filter fields including search".

Example of the updated round-trip test:

```ts
  it('round-trips all eight filter fields including search', () => {
    const filters: OpsFilters = {
      statuses: ['todo', 'doing'],
      priorities: [1, 2],
      projectId: 'prj_abc',
      sprintId: 'spr_xyz',
      epicId: 'epc_123',
      tagSlug: 'infra',
      manual: 'auto',
      search: 'scheduler',
    }
    saveOpsFilters(filters)
    const restored = readOpsFilters()
    expect(restored).toEqual(filters)
  })
```

- [ ] **Step 1.5: Update `task-list-cursor.ts`**

Edit `apps/gui/src/lib/task-list-cursor.ts`. Drop the `mode` field and add `search`:

```ts
import type { TaskStatus } from './types'
import { parseManualFilter, type ManualFilter } from './ops-filters-storage'

const KEY = 'torque:task-list-cursor'

export interface CursorFilter {
  statuses: TaskStatus[]
  priorities: number[]
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  manual: ManualFilter
  search: string
}

export interface TaskListCursor {
  ids: string[]
  filter: CursorFilter | null
}

export function saveTaskListCursor(ids: string[], filter: CursorFilter | null = null): void {
  try {
    const payload: TaskListCursor = { ids, filter }
    sessionStorage.setItem(KEY, JSON.stringify(payload))
  } catch {
    // sessionStorage may be unavailable (private mode / quota) — fail quiet
  }
}

export function readTaskListCursor(): TaskListCursor {
  const empty: TaskListCursor = { ids: [], filter: null }
  try {
    const raw = sessionStorage.getItem(KEY)
    if (!raw) return empty
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return empty
    const obj = parsed as Record<string, unknown>
    const ids = Array.isArray(obj.ids)
      ? (obj.ids as unknown[]).filter((v): v is string => typeof v === 'string')
      : []
    const filter = parseFilter(obj.filter)
    return { ids, filter }
  } catch {
    return empty
  }
}

export function clearTaskListCursor(): void {
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // ignore
  }
}

function parseFilter(raw: unknown): CursorFilter | null {
  if (!raw || typeof raw !== 'object') return null
  const f = raw as Record<string, unknown>
  const statuses = Array.isArray(f.statuses)
    ? (f.statuses as unknown[]).filter((s): s is TaskStatus => typeof s === 'string') as TaskStatus[]
    : []
  const priorities = Array.isArray(f.priorities)
    ? (f.priorities as unknown[]).filter((p): p is number => typeof p === 'number')
    : []
  return {
    statuses,
    priorities,
    projectId: typeof f.projectId === 'string' ? f.projectId : null,
    sprintId: typeof f.sprintId === 'string' ? f.sprintId : null,
    epicId: typeof f.epicId === 'string' ? f.epicId : null,
    tagSlug: typeof f.tagSlug === 'string' ? f.tagSlug : null,
    manual: parseManualFilter(f.manual),
    search: typeof f.search === 'string' ? f.search : '',
  }
}
```

- [ ] **Step 1.6: Update `BoardPage.tsx` — remove Mode, rename `'all'` → `'both'`, add search placeholder**

Open `apps/gui/src/pages/BoardPage.tsx`. Apply the following edits:

1. **Remove Mode imports and helpers.** Delete `MODE_PRESETS` from the `@/lib/constants` import, delete the `ModePreset` type alias, delete `MODE_VALUES` const, delete `parseModeParam` function.

   Change line 16 from:
   ```ts
   import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS, TASK_STATUSES } from '@/lib/constants'
   ```
   to:
   ```ts
   import { DEFAULT_ACTIVE_STATUSES, TASK_STATUSES } from '@/lib/constants'
   ```

   Delete lines 27–32 (the `FILTER_PARAM_KEYS` const, `ModePreset` type, `SSE_EVENTS`, `MODE_VALUES`), then re-add `FILTER_PARAM_KEYS` and `SSE_EVENTS` without `mode`:
   ```ts
   const FILTER_PARAM_KEYS = ['status', 'priority', 'project_id', 'sprint_id', 'epic_id', 'tag', 'manual'] as const

   const SSE_EVENTS = ['task.created', 'task.updated', 'task.transitioned']
   ```

   Delete `parseModeParam` (lines 52–55).

2. **Remove `mode` URL derivation** (lines 83–86).

3. **Remove `handleModeChange`** (lines 331–341).

4. **Update hydration effect** (around line 176): delete the `if (stored.mode && stored.mode !== 'all')` branch.

5. **Update the persist effect** (lines 195–207): remove `mode` from the saved object; add `search: ''` placeholder (wired for real in Task 9):
   ```ts
   useEffect(() => {
     if (!hydrated) return
     saveOpsFilters({
       statuses: activeStatuses,
       priorities: activePriorities,
       projectId,
       sprintId,
       epicId,
       tagSlug,
       manual: manualFilter,
       search: '',
     })
   }, [hydrated, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter])
   ```

6. **Update `fetchTasks`** (line 270): replace the `'all'` comparison:
   ```ts
   manual: manualFilter === 'both' ? undefined : manualFilter === 'manual',
   ```

7. **Update `handleManualFilterChange`** (line 352):
   ```ts
   function handleManualFilterChange(value: ManualFilter) {
     updateParams((p) => {
       if (value === 'both') p.delete('manual')
       else p.set('manual', value)
     })
   }
   ```

8. **Update `filtersActive`** (lines 366–374): drop the `mode !== 'all'` check; change `manualFilter !== 'all'` to `manualFilter !== 'both'`:
   ```ts
   const filtersActive =
     activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ||
     activePriorities.length > 0 ||
     projectId !== null ||
     sprintId !== null ||
     epicId !== null ||
     tagSlug !== null ||
     manualFilter !== 'both'
   ```

9. **Update `cursorFilter`** (lines 390–402): drop `mode`, add `search: ''` placeholder:
   ```ts
   const cursorFilter = useMemo(
     () => ({
       statuses: activeStatuses,
       priorities: activePriorities,
       projectId,
       sprintId,
       epicId,
       tagSlug,
       manual: manualFilter,
       search: '',
     }),
     [activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter]
   )
   ```

10. **Remove `mode` and `onModeChange` from the `<FilterBar>` JSX** (lines 427–462): delete those two props. Also delete the `mode` variable usage in the JSX.

    The FilterBar JSX should no longer reference `mode`/`onModeChange`. Everything else stays.

- [ ] **Step 1.7: Update `filter-bar.tsx` MANUAL_OPTIONS only**

In `apps/gui/src/components/domain/filter-bar.tsx`, update just the `MANUAL_OPTIONS` constant and the default fallback. (Full redesign lands later.)

Replace the block at lines 18–22:
```ts
const MANUAL_OPTIONS: ReadonlyArray<{ value: ManualFilter; label: string; title: string }> = [
  { value: 'both', label: 'Both', title: 'All tasks (no manual filter)' },
  { value: 'auto', label: 'Auto', title: 'Scheduler-eligible (manual=false)' },
  { value: 'manual', label: 'Manual', title: 'Held for review (manual=true)' },
]
```

Update the default fallback at line 172:
```ts
const active = (manualFilter ?? 'both') === opt.value
```

**Temporarily, also update FilterBar's `mode` prop to be optional and default-handled** so `BoardPage` no longer passing it doesn't type-error. Change the prop contract:

```ts
interface FilterBarProps {
  activeStatuses: TaskStatus[]
  onStatusToggle: (status: TaskStatus) => void
  mode?: ModePreset                              // now optional
  onModeChange?: (mode: ModePreset) => void     // now optional
  // ... rest unchanged
}
```

And guard the Mode preset rendering (lines 133–161) so it only renders when `onModeChange` is provided:

```tsx
      {/* Mode presets — only rendered when BoardPage passes the handler.
          (To be removed entirely in Task 8 once callers stop passing it.) */}
      {onModeChange && (
        <div className="flex items-center gap-1 border-l border-zinc-800 pl-3">
          {/* existing Mode content unchanged */}
        </div>
      )}
```

(This guard is temporary — Task 8 deletes the whole block. The only reason it exists now is so that Sprint/Project/Epic detail pages, which also use `FilterBar` and currently pass `mode`/`onModeChange`, keep working without a separate migration in this task.)

Check Sprint/Project/Epic detail pages — they still pass `mode` and `onModeChange`, so keeping the conditional rendering in FilterBar is load-bearing until Task 8.

- [ ] **Step 1.8: Update `BoardPage.rehydrate.test.tsx`**

In `apps/gui/src/pages/BoardPage.rehydrate.test.tsx`, every `saveOpsFilters({...})` literal needs `mode` removed, `manual: 'all'` → `manual: 'both'`, and `search: ''` added. Five fixtures total. Example:

```ts
    saveOpsFilters({
      statuses: ['todo'],
      priorities: [1],
      projectId: 'prj_abc',
      sprintId: null,
      epicId: null,
      tagSlug: null,
      manual: 'both',
      search: '',
    })
```

Apply the same transformation to the other four fixtures in the file (`statuses: ['todo'] + priorities: []`, `statuses: ['doing']`, `DEFAULT_ACTIVE_STATUSES`, and the deep-link test).

- [ ] **Step 1.9: Run all tests — must pass**

```bash
cd apps/gui
npm run test:run
```

Expected: all pre-existing tests pass, plus the two new migration tests. Zero failures.

- [ ] **Step 1.10: Typecheck**

```bash
cd apps/gui
npx tsc -b
```

Expected: no errors.

- [ ] **Step 1.11: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/lib/ops-filters-storage.ts \
        apps/gui/src/lib/ops-filters-storage.test.ts \
        apps/gui/src/lib/task-list-cursor.ts \
        apps/gui/src/pages/BoardPage.tsx \
        apps/gui/src/pages/BoardPage.rehydrate.test.tsx \
        apps/gui/src/components/domain/filter-bar.tsx
git commit -m "refactor(ops): rename manual='all' to 'both', drop mode from storage, add search field"
```

---

## Task 2: Move `filter-bar.tsx` into `filter-bar/` directory

Structural move only — no behavior change. After this task, all four existing callers keep importing `@/components/domain/filter-bar` and continue to work.

**Files:**
- Create: `apps/gui/src/components/domain/filter-bar/filter-bar.tsx`
- Create: `apps/gui/src/components/domain/filter-bar/index.ts`
- Delete: `apps/gui/src/components/domain/filter-bar.tsx`

- [ ] **Step 2.1: Create the new directory and copy the file**

```bash
cd ~/Projects-apps/torque/apps/gui/src/components/domain
mkdir filter-bar
git mv filter-bar.tsx filter-bar/filter-bar.tsx
```

- [ ] **Step 2.2: Create `index.ts` re-export**

Write `apps/gui/src/components/domain/filter-bar/index.ts`:

```ts
export { FilterBar } from './filter-bar'
```

- [ ] **Step 2.3: Typecheck and test**

```bash
cd apps/gui
npx tsc -b
npm run test:run
```

Expected: both green. All four callers (`BoardPage`, `SprintDetailPage`, `ProjectDetailPage`, `EpicDetailPage`) still resolve `@/components/domain/filter-bar`.

- [ ] **Step 2.4: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/
git commit -m "refactor(ops): move filter-bar.tsx into filter-bar/ directory with index re-export"
```

---

## Task 3: `FilterCycleToggle` primitive

A generic cycle button. Clicking advances the value through an ordered options array, wrapping back to the first. Used by Manual in this task (Task 4) and potentially future state-bearing toggles.

**Files:**
- Create: `apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.tsx`
- Create: `apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.test.tsx`

- [ ] **Step 3.1: Write the failing test**

Write `apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.test.tsx`:

```tsx
/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FilterCycleToggle } from './filter-cycle-toggle'

type V = 'both' | 'auto' | 'manual'

const OPTIONS: ReadonlyArray<{ value: V; label: string; dotColor?: string }> = [
  { value: 'both', label: 'Both', dotColor: 'bg-zinc-400' },
  { value: 'auto', label: 'Auto', dotColor: 'bg-blue-400' },
  { value: 'manual', label: 'Manual', dotColor: 'bg-amber-400' },
]

describe('FilterCycleToggle', () => {
  it('renders the label for the current value', () => {
    render(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={() => {}}
        ariaLabel="Manual filter"
      />,
    )
    expect(screen.getByRole('button', { name: /manual filter/i })).toHaveTextContent('Auto')
  })

  it('advances to the next option on click and wraps at the end', () => {
    const onChange = vi.fn<(v: V) => void>()
    const { rerender } = render(
      <FilterCycleToggle
        options={OPTIONS}
        value="both"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('auto')

    rerender(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('manual')

    rerender(
      <FilterCycleToggle
        options={OPTIONS}
        value="manual"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('both')
  })

  it('falls back to first option if current value is not in options', () => {
    const onChange = vi.fn<(v: V) => void>()
    render(
      <FilterCycleToggle
        options={OPTIONS}
        // Cast: simulates a corrupted state value.
        value={'bogus' as unknown as V}
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    // When current value isn't found, advancing from index 0 → 1 (next after "both").
    expect(onChange).toHaveBeenLastCalledWith('auto')
  })

  it('renders the dot with the active option color class', () => {
    const { container } = render(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={() => {}}
        ariaLabel="Manual filter"
      />,
    )
    const dot = container.querySelector('[data-testid="cycle-dot"]')
    expect(dot).not.toBeNull()
    expect(dot?.className).toContain('bg-blue-400')
  })
})
```

- [ ] **Step 3.2: Run the test — must fail**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-cycle-toggle.test.tsx
```

Expected: `Cannot find module './filter-cycle-toggle'`.

- [ ] **Step 3.3: Implement the component**

Write `apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.tsx`:

```tsx
export interface CycleOption<T extends string> {
  value: T
  label: string
  /** Tailwind bg- class for the leading dot; omit for no dot. */
  dotColor?: string
  /** title attribute for the button (tooltip). */
  title?: string
}

interface FilterCycleToggleProps<T extends string> {
  options: ReadonlyArray<CycleOption<T>>
  value: T
  onChange: (next: T) => void
  ariaLabel: string
}

export function FilterCycleToggle<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: FilterCycleToggleProps<T>) {
  const idx = options.findIndex((o) => o.value === value)
  // If the current value isn't found, treat it as "before the first option" so
  // the next click lands on index 0's successor. This keeps corrupted state
  // recoverable via a single click instead of trapping the user.
  const active = idx >= 0 ? options[idx] : options[0]
  const nextIdx = idx >= 0 ? (idx + 1) % options.length : 1 % options.length
  const next = options[nextIdx]

  return (
    <button
      type="button"
      aria-label={`${ariaLabel}, current: ${active?.label ?? 'unset'}`}
      title={active?.title ?? `${ariaLabel}: ${active?.label ?? ''}`}
      onClick={() => onChange(next.value)}
      className="inline-flex items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-zinc-200 transition-all hover:border-zinc-600"
    >
      {active?.dotColor && (
        <span
          data-testid="cycle-dot"
          className={`inline-block h-1.5 w-1.5 rounded-full ${active.dotColor}`}
        />
      )}
      <span>{active?.label ?? ''}</span>
    </button>
  )
}
```

- [ ] **Step 3.4: Run the test — must pass**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-cycle-toggle.test.tsx
```

Expected: 4 passing.

- [ ] **Step 3.5: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.tsx \
        apps/gui/src/components/domain/filter-bar/filter-cycle-toggle.test.tsx
git commit -m "feat(ops): add FilterCycleToggle primitive"
```

---

## Task 4: Use `FilterCycleToggle` for Manual in `FilterBar`

Replace the three-button MANUAL_OPTIONS group in `filter-bar.tsx` with a single `FilterCycleToggle`. No contract change — `FilterBar`'s `manualFilter` / `onManualFilterChange` props stay.

**Files:**
- Modify: `apps/gui/src/components/domain/filter-bar/filter-bar.tsx`

- [ ] **Step 4.1: Replace the Manual button group**

In `apps/gui/src/components/domain/filter-bar/filter-bar.tsx`:

1. Remove the top-level `MANUAL_OPTIONS` const (around lines 18–22) — it's being inlined into the cycle-toggle options.

2. Add the import:
   ```ts
   import { FilterCycleToggle, type CycleOption } from './filter-cycle-toggle'
   ```

3. Define cycle options at the module level (replaces the removed `MANUAL_OPTIONS`):
   ```ts
   const MANUAL_CYCLE_OPTIONS: ReadonlyArray<CycleOption<ManualFilter>> = [
     { value: 'both', label: 'Both', dotColor: 'bg-zinc-400', title: 'All tasks (no manual filter)' },
     { value: 'auto', label: 'Auto', dotColor: 'bg-blue-400', title: 'Scheduler-eligible (manual=false)' },
     { value: 'manual', label: 'Manual', dotColor: 'bg-amber-400', title: 'Held for review (manual=true)' },
   ]
   ```

4. Replace the entire `{onManualFilterChange && (...)}` block (lines ~164–192) with:
   ```tsx
       {/* Manual-flag cycle: Both → Auto → Manual */}
       {onManualFilterChange && (
         <div className="flex items-center gap-1 border-l border-zinc-800 pl-3">
           <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Manual:</span>
           <FilterCycleToggle
             options={MANUAL_CYCLE_OPTIONS}
             value={manualFilter ?? 'both'}
             onChange={onManualFilterChange}
             ariaLabel="Manual filter"
           />
         </div>
       )}
   ```

- [ ] **Step 4.2: Run tests**

```bash
cd apps/gui
npm run test:run
```

Expected: all green. (The cycle-toggle unit tests plus existing BoardPage tests.)

- [ ] **Step 4.3: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-bar.tsx
git commit -m "feat(ops): swap Manual tri-state chips for FilterCycleToggle"
```

---

## Task 5: `FilterEntityCombobox` primitive

A shadcn `Popover` + `Command` combobox. Trigger shows icon + entity name (or muted "All X" when unselected). Popover opens a typeahead list with an always-present "All X" row and optional "Create" footer.

**Files:**
- Create: `apps/gui/src/components/domain/filter-bar/filter-entity-combobox.tsx`
- Create: `apps/gui/src/components/domain/filter-bar/filter-entity-combobox.test.tsx`

- [ ] **Step 5.1: Write the failing test**

Write `apps/gui/src/components/domain/filter-bar/filter-entity-combobox.test.tsx`:

```tsx
/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FilterEntityCombobox } from './filter-entity-combobox'

const ITEMS = [
  { id: 'prj_a', name: 'Alpha' },
  { id: 'prj_b', name: 'Beta' },
  { id: 'prj_c', name: 'Charlie' },
]

describe('FilterEntityCombobox', () => {
  it('renders the muted "all" label when value is null', () => {
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    expect(screen.getByRole('button', { name: /filter by project/i })).toHaveTextContent('All projects')
  })

  it('renders the selected item name when value is set', () => {
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value="prj_b"
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    expect(screen.getByRole('button', { name: /filter by project/i })).toHaveTextContent('Beta')
  })

  it('opens a popover with all items on click and selects one', () => {
    const onChange = vi.fn<(id: string | null) => void>()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={onChange}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    // Popover opens; verify items are visible.
    expect(screen.getByText('All projects')).toBeInTheDocument()
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Beta'))
    expect(onChange).toHaveBeenCalledWith('prj_b')
  })

  it('selecting the all-row calls onChange(null)', () => {
    const onChange = vi.fn<(id: string | null) => void>()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value="prj_b"
        onChange={onChange}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    fireEvent.click(screen.getByText('All projects'))
    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('renders a Create footer when onCreate is provided', () => {
    const onCreate = vi.fn()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
        onCreate={onCreate}
        createLabel="New project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    const createBtn = screen.getByText('New project')
    fireEvent.click(createBtn)
    expect(onCreate).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 5.2: Run the test — must fail**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-entity-combobox.test.tsx
```

Expected: module-not-found failure.

- [ ] **Step 5.3: Implement the component**

Write `apps/gui/src/components/domain/filter-bar/filter-entity-combobox.tsx`:

```tsx
import { useState, type ReactNode } from 'react'
import { ChevronDown, Plus } from 'lucide-react'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

export interface FilterEntityComboboxItem {
  id: string
  name: string
}

interface FilterEntityComboboxProps {
  icon: ReactNode
  items: FilterEntityComboboxItem[]
  value: string | null
  onChange: (id: string | null) => void
  allLabel: string
  ariaLabel: string
  onCreate?: () => void
  createLabel?: string
}

export function FilterEntityCombobox({
  icon,
  items,
  value,
  onChange,
  allLabel,
  ariaLabel,
  onCreate,
  createLabel,
}: FilterEntityComboboxProps) {
  const [open, setOpen] = useState(false)
  const selected = value ? items.find((i) => i.id === value) : null
  const displayLabel = selected?.name ?? allLabel
  const isMuted = selected === null || selected === undefined

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          role="button"
          aria-label={ariaLabel}
          aria-expanded={open}
          className="inline-flex h-7 items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[10px] tracking-wider transition-colors hover:border-zinc-600"
        >
          <span className="text-zinc-400">{icon}</span>
          <span className={isMuted ? 'text-zinc-500' : 'text-zinc-100'}>{displayLabel}</span>
          <ChevronDown className="h-3 w-3 text-zinc-600" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command>
          <CommandInput placeholder="Search…" className="h-8 text-[11px]" />
          <CommandList>
            <CommandEmpty>No results.</CommandEmpty>
            <CommandGroup>
              <CommandItem
                value="__all__"
                onSelect={() => {
                  onChange(null)
                  setOpen(false)
                }}
              >
                <span className="text-zinc-400">{allLabel}</span>
              </CommandItem>
              {items.map((item) => (
                <CommandItem
                  key={item.id}
                  value={item.name}
                  onSelect={() => {
                    onChange(item.id)
                    setOpen(false)
                  }}
                >
                  {item.name}
                </CommandItem>
              ))}
            </CommandGroup>
            {onCreate && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <CommandItem
                    value="__create__"
                    onSelect={() => {
                      onCreate()
                      setOpen(false)
                    }}
                  >
                    <Plus className="mr-1.5 h-3 w-3" />
                    {createLabel ?? 'Create new'}
                  </CommandItem>
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
```

- [ ] **Step 5.4: Run the test — must pass**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-entity-combobox.test.tsx
```

Expected: 5 passing.

If any tests fail due to shadcn Popover's use of portals (items may not be in the queried container on first render), wrap the asserts in `await screen.findByText(...)` instead of the sync `getByText(...)` — the popover renders async on open. Sample replacement:
```ts
fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
expect(await screen.findByText('Alpha')).toBeInTheDocument()
```

- [ ] **Step 5.5: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-entity-combobox.tsx \
        apps/gui/src/components/domain/filter-bar/filter-entity-combobox.test.tsx
git commit -m "feat(ops): add FilterEntityCombobox primitive"
```

---

## Task 6: Use `FilterEntityCombobox` for group selectors

Replace the internal `GroupSelect` helper + its shadcn `Select` usage with `FilterEntityCombobox` for Project, Sprint, Epic, Tag. Delete `GroupSelect`.

**Files:**
- Modify: `apps/gui/src/components/domain/filter-bar/filter-bar.tsx`

- [ ] **Step 6.1: Add icons + Combobox import**

In `filter-bar.tsx`, replace the existing `import { Plus } from 'lucide-react'` line with:

```ts
import { Folder, Calendar, BookOpen, Hash } from 'lucide-react'
import { FilterEntityCombobox } from './filter-entity-combobox'
```

Also delete the Select imports (no longer used):
```ts
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
```

And delete the `ALL_VALUE` constant at the top.

- [ ] **Step 6.2: Replace the four `GroupSelect` usages**

Inside the `{showGroups && (...)}` block (lines ~195–242), replace the four `<GroupSelect ... />` invocations with:

```tsx
          {onProjectChange && (
            <FilterEntityCombobox
              icon={<Folder className="h-3.5 w-3.5" />}
              items={projects ?? []}
              value={projectId ?? null}
              onChange={onProjectChange}
              allLabel="All projects"
              ariaLabel="Filter by project"
              onCreate={onProjectCreate}
              createLabel="New project"
            />
          )}
          {onSprintChange && (
            <FilterEntityCombobox
              icon={<Calendar className="h-3.5 w-3.5" />}
              items={sprints ?? []}
              value={sprintId ?? null}
              onChange={onSprintChange}
              allLabel="All sprints"
              ariaLabel="Filter by sprint"
            />
          )}
          {onEpicChange && (
            <FilterEntityCombobox
              icon={<BookOpen className="h-3.5 w-3.5" />}
              items={epics ?? []}
              value={epicId ?? null}
              onChange={onEpicChange}
              allLabel="All epics"
              ariaLabel="Filter by epic"
              onCreate={onEpicCreate}
              createLabel="New epic"
            />
          )}
          {onTagChange && (
            <FilterEntityCombobox
              icon={<Hash className="h-3.5 w-3.5" />}
              items={(tags ?? []).map((t) => ({ id: t.slug, name: t.name }))}
              value={tagSlug ?? null}
              onChange={onTagChange}
              allLabel="All tags"
              ariaLabel="Filter by tag"
            />
          )}
```

Note: sprints and epics are already passed with `id` and `name` fields, so they satisfy `FilterEntityComboboxItem` directly. Only tags need the slug→id mapping.

- [ ] **Step 6.3: Delete the `GroupSelect` helper**

Delete the entire `GroupSelect` function and `GroupItem` interface at the bottom of `filter-bar.tsx` (lines ~249–309).

- [ ] **Step 6.4: Typecheck and test**

```bash
cd apps/gui
npx tsc -b
npm run test:run
```

Expected: all green.

- [ ] **Step 6.5: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-bar.tsx
git commit -m "feat(ops): swap GroupSelect dropdowns for FilterEntityCombobox"
```

---

## Task 7: `FilterSearchInput` primitive

Debounced search input with `/` focus shortcut and `Esc` clear. Maintains local state for instant input responsiveness; emits `onChange` after 250ms idle.

**Files:**
- Create: `apps/gui/src/components/domain/filter-bar/filter-search-input.tsx`
- Create: `apps/gui/src/components/domain/filter-bar/filter-search-input.test.tsx`

- [ ] **Step 7.1: Write the failing test**

Write `apps/gui/src/components/domain/filter-bar/filter-search-input.test.tsx`:

```tsx
/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { FilterSearchInput } from './filter-search-input'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('FilterSearchInput', () => {
  it('debounces onChange by 250ms', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="" onChange={onChange} />)
    const input = screen.getByRole('searchbox')
    fireEvent.change(input, { target: { value: 's' } })
    fireEvent.change(input, { target: { value: 'sc' } })
    fireEvent.change(input, { target: { value: 'sch' } })

    // Not yet — still under the debounce window.
    expect(onChange).not.toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenLastCalledWith('sch')
  })

  it('does not emit when typing matches the current value (no spurious fires)', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="foo" onChange={onChange} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    expect(input.value).toBe('foo')

    // No local changes, no emission.
    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('Esc clears the input and blurs', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="foo" onChange={onChange} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    input.focus()
    expect(document.activeElement).toBe(input)

    fireEvent.keyDown(input, { key: 'Escape' })
    expect(input.value).toBe('')
    expect(document.activeElement).not.toBe(input)

    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).toHaveBeenLastCalledWith('')
  })

  it('/ key focuses the input when no other input is focused', () => {
    render(<FilterSearchInput value="" onChange={() => {}} />)
    const input = screen.getByRole('searchbox')
    expect(document.activeElement).not.toBe(input)

    fireEvent.keyDown(window, { key: '/' })
    expect(document.activeElement).toBe(input)
  })

  it('/ key is a no-op when another input is focused', () => {
    render(
      <>
        <input data-testid="other" />
        <FilterSearchInput value="" onChange={() => {}} />
      </>,
    )
    const other = screen.getByTestId('other')
    const search = screen.getByRole('searchbox')
    other.focus()
    expect(document.activeElement).toBe(other)

    fireEvent.keyDown(window, { key: '/' })
    // Other input stays focused; search did not steal.
    expect(document.activeElement).toBe(other)
    expect(document.activeElement).not.toBe(search)
  })

  it('external value changes update the displayed text', () => {
    const { rerender } = render(<FilterSearchInput value="old" onChange={() => {}} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    expect(input.value).toBe('old')

    rerender(<FilterSearchInput value="new" onChange={() => {}} />)
    expect(input.value).toBe('new')
  })
})
```

- [ ] **Step 7.2: Run the test — must fail**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-search-input.test.tsx
```

Expected: module-not-found failure.

- [ ] **Step 7.3: Implement the component**

Write `apps/gui/src/components/domain/filter-bar/filter-search-input.tsx`:

```tsx
import { useEffect, useRef, useState } from 'react'
import { Search } from 'lucide-react'

interface FilterSearchInputProps {
  value: string
  onChange: (next: string) => void
  placeholder?: string
}

const DEBOUNCE_MS = 250

function isEditableTarget(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) return true
  if (el instanceof HTMLElement && el.isContentEditable) return true
  return false
}

export function FilterSearchInput({
  value,
  onChange,
  placeholder = 'Search tasks by title or description…',
}: FilterSearchInputProps) {
  const [local, setLocal] = useState(value)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastEmittedRef = useRef(value)

  // Keep local value in sync when the prop changes externally (e.g. Clear).
  useEffect(() => {
    setLocal(value)
    lastEmittedRef.current = value
  }, [value])

  // Debounced emit. Only fires when local !== last-emitted to avoid churn.
  useEffect(() => {
    if (local === lastEmittedRef.current) return
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      lastEmittedRef.current = local
      onChange(local)
    }, DEBOUNCE_MS)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [local, onChange])

  // `/` focuses the input when no other editable element holds focus.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== '/') return
      if (isEditableTarget(document.activeElement)) return
      e.preventDefault()
      inputRef.current?.focus()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  return (
    <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded border border-zinc-800 bg-zinc-900/50 px-2.5 py-1 focus-within:border-zinc-600">
      <Search className="h-3.5 w-3.5 text-zinc-500" />
      <input
        ref={inputRef}
        type="search"
        aria-label="Search tasks"
        placeholder={placeholder}
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setLocal('')
            inputRef.current?.blur()
          }
        }}
        className="flex-1 bg-transparent text-xs text-zinc-100 placeholder:text-zinc-600 focus:outline-none"
      />
      <kbd className="rounded border border-zinc-800 bg-zinc-950 px-1 text-[9px] uppercase tracking-wider text-zinc-500">/</kbd>
    </div>
  )
}
```

- [ ] **Step 7.4: Run the test — must pass**

```bash
cd apps/gui
npm run test:run -- src/components/domain/filter-bar/filter-search-input.test.tsx
```

Expected: 6 passing.

- [ ] **Step 7.5: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-search-input.tsx \
        apps/gui/src/components/domain/filter-bar/filter-search-input.test.tsx
git commit -m "feat(ops): add FilterSearchInput primitive with debounce + / shortcut"
```

---

## Task 8: `FilterBar` two-row layout with summary row + integrated Clear

Add a conditional two-row rendering mode to `FilterBar`. When the caller provides `searchQuery` and `onSearchChange`, the component renders:

- **Row 1:** `FilterSearchInput`, right-aligned summary (`N filters`, `M matches`, `N filters · M matches`, or hidden), and `Clear` button (visible only when at least one filter is non-default OR search is non-empty).
- **Row 2:** the existing chip row (status + priority + manual cycle + entity comboboxes).

When `searchQuery` is absent, the single-row layout stays exactly as it is today (Sprint/Project/Epic detail pages unaffected).

Also: at this point we remove Mode preset from `FilterBar`'s interface and UI — no caller passes it after Task 1. This is the Task 8 cleanup promised earlier.

**Files:**
- Modify: `apps/gui/src/components/domain/filter-bar/filter-bar.tsx`

- [ ] **Step 8.1: Extend `FilterBar` props**

In `filter-bar.tsx`, update `FilterBarProps`:

```ts
interface FilterBarProps {
  activeStatuses: TaskStatus[]
  onStatusToggle: (status: TaskStatus) => void
  /** Available statuses to show — defaults to all TASK_STATUSES */
  availableStatuses?: readonly TaskStatus[]
  /** Show priority filter chips */
  activePriorities?: number[]
  onPriorityToggle?: (priority: number) => void
  /** Manual-flag cycle (Both / Auto / Manual). Omit to hide the control. */
  manualFilter?: ManualFilter
  onManualFilterChange?: (value: ManualFilter) => void
  /** Project / Sprint / Epic / Tag combobox selectors (all optional) */
  projects?: Project[]
  projectId?: string | null
  onProjectChange?: (id: string | null) => void
  sprints?: Sprint[]
  sprintId?: string | null
  onSprintChange?: (id: string | null) => void
  epics?: Epic[]
  epicId?: string | null
  onEpicChange?: (id: string | null) => void
  tags?: Tag[]
  tagSlug?: string | null
  onTagChange?: (slug: string | null) => void
  onProjectCreate?: () => void
  onEpicCreate?: () => void

  /** When provided (both together), the two-row layout with search renders. */
  searchQuery?: string
  onSearchChange?: (q: string) => void
  /** Count of rows matched by the current search (from the caller). */
  searchMatchCount?: number
  /** Count of non-default filter dimensions (from the caller; see design spec summary rules). */
  activeFilterCount?: number
  /** Invoked when the Clear button is clicked. Omit to hide the button entirely. */
  onClear?: () => void

  /** Extra trailing controls in single-row mode (backwards-compat slot). Ignored in two-row mode. */
  children?: ReactNode
}
```

Remove `mode`, `onModeChange`, and `ModePreset` from the props and from the top of the file.

- [ ] **Step 8.2: Delete Mode-related code**

In `filter-bar.tsx`:
- Remove the `import { MODE_PRESETS, ... }` line's `MODE_PRESETS` — keep only `STATUS_COLORS, DEFAULT_STATUS_COLOR, PRIORITIES, TASK_STATUSES`.
- Remove the `ModePreset` type alias (top of file).
- Remove the entire `{/* Mode presets */}` block from the JSX (the `{onModeChange && (...)}` guard added in Task 1 and the surrounding div).

- [ ] **Step 8.3: Add the search-mode import**

Top of `filter-bar.tsx`:

```ts
import { FilterSearchInput } from './filter-search-input'
```

- [ ] **Step 8.4: Split the rendering into two branches**

Restructure the `FilterBar` function body. The chip-row JSX becomes a local constant; the component returns the two-row shell if `searchQuery != null && onSearchChange != null`, otherwise the single-row shell.

```tsx
export function FilterBar({
  activeStatuses,
  onStatusToggle,
  availableStatuses = TASK_STATUSES,
  activePriorities = [],
  onPriorityToggle,
  manualFilter,
  onManualFilterChange,
  projects,
  projectId,
  onProjectChange,
  sprints,
  sprintId,
  onSprintChange,
  epics,
  epicId,
  onEpicChange,
  tags,
  tagSlug,
  onTagChange,
  onProjectCreate,
  onEpicCreate,
  searchQuery,
  onSearchChange,
  searchMatchCount,
  activeFilterCount = 0,
  onClear,
  children,
}: FilterBarProps) {
  const showGroups = Boolean(onProjectChange || onSprintChange || onEpicChange || onTagChange)
  const twoRow = searchQuery !== undefined && onSearchChange !== undefined

  const chipRow = (
    <div className="flex flex-wrap items-center gap-3 text-xs">
      {/* Status chips */}
      <div className="flex flex-wrap items-center gap-1">
        <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Status:</span>
        {availableStatuses.map((status) => {
          const active = activeStatuses.includes(status)
          const colors = STATUS_COLORS[status] ?? DEFAULT_STATUS_COLOR
          return (
            <button
              key={status}
              type="button"
              onClick={() => onStatusToggle(status)}
              className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
                active
                  ? `${colors.border} ${colors.bg} ${colors.text} ring-1 ring-white/20`
                  : 'border-zinc-800 bg-zinc-900/50 text-zinc-600 hover:text-zinc-400 opacity-50'
              }`}
            >
              {status}
            </button>
          )
        })}
      </div>

      {/* Priority chips */}
      {onPriorityToggle && (
        <div className="flex items-center gap-1">
          <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Priority:</span>
          {PRIORITIES.map(({ value, label }) => {
            const active = activePriorities.includes(value)
            return (
              <button
                key={value}
                type="button"
                onClick={() => onPriorityToggle(value)}
                className={`rounded border px-2 py-0.5 text-[10px] font-medium tracking-wider transition-all ${
                  active
                    ? 'border-amber-500/40 bg-amber-500/10 text-amber-200'
                    : 'border-zinc-800 bg-zinc-900/50 text-zinc-600 hover:text-zinc-400'
                }`}
              >
                {label}
              </button>
            )
          })}
        </div>
      )}

      {/* Manual cycle */}
      {onManualFilterChange && (
        <div className="flex items-center gap-1 border-l border-zinc-800 pl-3">
          <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Manual:</span>
          <FilterCycleToggle
            options={MANUAL_CYCLE_OPTIONS}
            value={manualFilter ?? 'both'}
            onChange={onManualFilterChange}
            ariaLabel="Manual filter"
          />
        </div>
      )}

      {/* Group combobox pills */}
      {showGroups && (
        <div className="flex flex-wrap items-center gap-2 border-l border-zinc-800 pl-3">
          {onProjectChange && (
            <FilterEntityCombobox
              icon={<Folder className="h-3.5 w-3.5" />}
              items={projects ?? []}
              value={projectId ?? null}
              onChange={onProjectChange}
              allLabel="All projects"
              ariaLabel="Filter by project"
              onCreate={onProjectCreate}
              createLabel="New project"
            />
          )}
          {onSprintChange && (
            <FilterEntityCombobox
              icon={<Calendar className="h-3.5 w-3.5" />}
              items={sprints ?? []}
              value={sprintId ?? null}
              onChange={onSprintChange}
              allLabel="All sprints"
              ariaLabel="Filter by sprint"
            />
          )}
          {onEpicChange && (
            <FilterEntityCombobox
              icon={<BookOpen className="h-3.5 w-3.5" />}
              items={epics ?? []}
              value={epicId ?? null}
              onChange={onEpicChange}
              allLabel="All epics"
              ariaLabel="Filter by epic"
              onCreate={onEpicCreate}
              createLabel="New epic"
            />
          )}
          {onTagChange && (
            <FilterEntityCombobox
              icon={<Hash className="h-3.5 w-3.5" />}
              items={(tags ?? []).map((t) => ({ id: t.slug, name: t.name }))}
              value={tagSlug ?? null}
              onChange={onTagChange}
              allLabel="All tags"
              ariaLabel="Filter by tag"
            />
          )}
        </div>
      )}
    </div>
  )

  if (twoRow) {
    const showClear = Boolean(onClear) && (activeFilterCount > 0 || (searchQuery ?? '').length > 0)
    const showSummary = activeFilterCount > 0 || (searchQuery ?? '').length > 0

    let summaryText = ''
    if (activeFilterCount > 0 && (searchQuery ?? '').length > 0) {
      summaryText = `${activeFilterCount} filter${activeFilterCount === 1 ? '' : 's'} · ${searchMatchCount ?? 0} match${searchMatchCount === 1 ? '' : 'es'}`
    } else if (activeFilterCount > 0) {
      summaryText = `${activeFilterCount} filter${activeFilterCount === 1 ? '' : 's'}`
    } else if ((searchQuery ?? '').length > 0) {
      summaryText = `${searchMatchCount ?? 0} match${searchMatchCount === 1 ? '' : 'es'}`
    }

    return (
      <div className="flex flex-col border-b border-zinc-800/80 bg-zinc-950">
        {/* Row 1: search hero + summary + clear */}
        <div className="flex items-center gap-3 px-4 py-2">
          <FilterSearchInput value={searchQuery ?? ''} onChange={onSearchChange!} />
          {showSummary && (
            <span className="whitespace-nowrap text-[10px] uppercase tracking-wider text-zinc-500">
              {summaryText}
            </span>
          )}
          {showClear && (
            <button
              type="button"
              onClick={onClear}
              aria-label="Clear all filters and search"
              className="rounded border border-zinc-700 bg-transparent px-2 py-1 text-[10px] uppercase tracking-wider text-zinc-300 transition-colors hover:border-zinc-500 hover:text-zinc-100"
            >
              Clear
            </button>
          )}
        </div>
        {/* Row 2: compact chip row */}
        <div className="border-t border-zinc-800/80 px-4 py-2">{chipRow}</div>
      </div>
    )
  }

  // Single-row (legacy) layout for detail pages.
  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5">
      {chipRow}
      {children && <div className="ml-auto flex items-center gap-2">{children}</div>}
    </div>
  )
}
```

- [ ] **Step 8.5: Typecheck and test**

```bash
cd apps/gui
npx tsc -b
npm run test:run
```

Expected: all green. The Sprint/Project/Epic detail page tests (if any smoke tests exist) still render single-row; BoardPage still renders single-row too until Task 9 wires the search props.

- [ ] **Step 8.6: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/components/domain/filter-bar/filter-bar.tsx
git commit -m "feat(ops): add two-row FilterBar layout with summary and integrated Clear"
```

---

## Task 9: Wire search into `BoardPage`

Add `search` React state to `BoardPage`. Pass it through to `FilterBar` (triggering two-row mode), pass to `api.listTasks`, persist via `saveOpsFilters`, hydrate from storage on mount, compute `activeFilterCount` and `searchMatchCount`, and move Clear into `FilterBar`'s integrated slot (delete the `children` Clear button).

**Files:**
- Modify: `apps/gui/src/pages/BoardPage.tsx`
- Modify: `apps/gui/src/pages/BoardPage.rehydrate.test.tsx` (add a rehydration test for search)

- [ ] **Step 9.1: Add search state and hydration**

In `BoardPage.tsx`:

1. Add the import (if not already):
   ```ts
   import { useState } from 'react'
   ```
   (already imported; no change.)

2. Inside the component, below the other `useState` calls (around line 97), add:
   ```ts
   const [search, setSearch] = useState<string>('')
   ```

3. In the hydration effect (the `useEffect` on `location.key`), after reading `stored`, hydrate `search` too. Inside the `if (stored)` branch, just before `setHydrated(true)`:
   ```ts
   if (stored.search) setSearch(stored.search)
   ```

   (This runs unconditionally once per navigation arrival, same lifecycle as URL hydration.)

- [ ] **Step 9.2: Include search in the persistence effect**

Update the persist `useEffect` to include `search`:

```ts
useEffect(() => {
  if (!hydrated) return
  saveOpsFilters({
    statuses: activeStatuses,
    priorities: activePriorities,
    projectId,
    sprintId,
    epicId,
    tagSlug,
    manual: manualFilter,
    search,
  })
}, [hydrated, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search])
```

- [ ] **Step 9.3: Pass `search` to `listTasks`**

Update `fetchTasks`:

```ts
const fetchTasks = useCallback(async () => {
  try {
    const result = await api.listTasks({
      status: activeStatuses,
      priority: activePriorities.length ? activePriorities : undefined,
      project_id: projectId ?? undefined,
      sprint_id: sprintId ?? undefined,
      epic_id: epicId ?? undefined,
      tags: tagSlug ? [tagSlug] : undefined,
      manual: manualFilter === 'both' ? undefined : manualFilter === 'manual',
      search: search || undefined,
    })
    setTasks(result.tasks)
    setError(null)
  } catch (err) {
    setError(err instanceof Error ? err.message : 'Failed to load tasks')
  } finally {
    setLoading(false)
  }
}, [api, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search])
```

- [ ] **Step 9.4: Compute filter count and match count; thread into `FilterBar`**

Above the `return`, add the computed values:

```ts
const activeFilterCount =
  (activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ? 1 : 0) +
  (activePriorities.length > 0 ? 1 : 0) +
  (manualFilter !== 'both' ? 1 : 0) +
  (projectId !== null ? 1 : 0) +
  (sprintId !== null ? 1 : 0) +
  (epicId !== null ? 1 : 0) +
  (tagSlug !== null ? 1 : 0)

const searchMatchCount = search ? tasks.length : undefined
```

Note: this is deliberately the count of filter *dimensions* with non-default state, not the count of chip selections. Matches the design spec's summary rules.

- [ ] **Step 9.5: Extend `handleClearFilters` to clear search; thread search props into `FilterBar`**

Replace `handleClearFilters` with:

```ts
function handleClearFilters() {
  clearOpsFilters()
  setSearch('')
  setSearchParams(
    (prev) => {
      const next = new URLSearchParams(prev)
      for (const k of FILTER_PARAM_KEYS) next.delete(k)
      return next
    },
    { replace: true }
  )
}
```

Then update the `<FilterBar>` JSX: remove the `children` slot (the old Clear button), add the new search props, remove the now-deleted `mode`/`onModeChange`:

```tsx
<FilterBar
  activeStatuses={activeStatuses}
  onStatusToggle={handleStatusToggle}
  activePriorities={activePriorities}
  onPriorityToggle={handlePriorityToggle}
  manualFilter={manualFilter}
  onManualFilterChange={handleManualFilterChange}
  projects={projects}
  projectId={projectId}
  onProjectChange={(id) => handleGroupChange('project_id', id)}
  onProjectCreate={() => setProjectCreateOpen(true)}
  sprints={visibleSprints}
  sprintId={sprintId}
  onSprintChange={(id) => handleGroupChange('sprint_id', id)}
  epics={visibleEpics}
  epicId={epicId}
  onEpicChange={(id) => handleGroupChange('epic_id', id)}
  onEpicCreate={() => setEpicCreateOpen(true)}
  tags={tags}
  tagSlug={tagSlug}
  onTagChange={(slug) => handleGroupChange('tag', slug)}
  searchQuery={search}
  onSearchChange={setSearch}
  searchMatchCount={searchMatchCount}
  activeFilterCount={activeFilterCount}
  onClear={handleClearFilters}
/>
```

Delete the closing `{filtersActive && ( <Button ... /> )}` block that previously sat inside `<FilterBar>` — Clear is integrated now.

- [ ] **Step 9.6: Update `cursorFilter` to include live search**

```ts
const cursorFilter = useMemo(
  () => ({
    statuses: activeStatuses,
    priorities: activePriorities,
    projectId,
    sprintId,
    epicId,
    tagSlug,
    manual: manualFilter,
    search,
  }),
  [activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search]
)
```

- [ ] **Step 9.7: Clean up unused imports in BoardPage**

The `Button` import from `@/components/ui/button` is no longer used inside BoardPage since Clear moved into FilterBar. Remove the import. (Confirm with `grep Button apps/gui/src/pages/BoardPage.tsx` — only keep imports still in use.)

- [ ] **Step 9.8: Add a rehydration test for search**

Append to `apps/gui/src/pages/BoardPage.rehydrate.test.tsx`:

```tsx
  it('restores search from storage on fresh mount', async () => {
    saveOpsFilters({
      statuses: [],
      priorities: [],
      projectId: null,
      sprintId: null,
      epicId: null,
      tagSlug: null,
      manual: 'both',
      search: 'scheduler',
    })

    const root = renderAppShell(container, '/operations', () => {})
    await act(async () => {
      await Promise.resolve()
    })

    // Search is React state, not URL — assert by rendering the search input
    // and confirming its value.
    const input = container.querySelector('input[type="search"]') as HTMLInputElement | null
    expect(input).not.toBeNull()
    expect(input?.value).toBe('scheduler')

    act(() => root.unmount())
  })
```

- [ ] **Step 9.9: Typecheck and run tests**

```bash
cd apps/gui
npx tsc -b
npm run test:run
```

Expected: all green. Task 9's rehydration test passes; all existing tests continue to pass; the Operations page now renders the two-row layout at runtime.

- [ ] **Step 9.10: Commit**

```bash
cd ~/Projects-apps/torque
git add apps/gui/src/pages/BoardPage.tsx \
        apps/gui/src/pages/BoardPage.rehydrate.test.tsx
git commit -m "feat(ops): wire search through BoardPage with debounced fetch + storage persistence"
```

---

## Task 10: Final verification — tests, types, manual smoke

**Files:** none modified; verification only.

- [ ] **Step 10.1: Run the full test suite**

```bash
cd apps/gui
npm run test:run
```

Expected: zero failures. Capture the counts (files / tests / passed). Any failure means back to the relevant task — do not paper over.

- [ ] **Step 10.2: Typecheck**

```bash
cd apps/gui
npx tsc -b
```

Expected: no errors.

- [ ] **Step 10.3: Lint (catch unused imports, hook-dep mistakes)**

```bash
cd apps/gui
npm run lint
```

Expected: zero errors. Warnings about hook dependency arrays deserve a close look — the BoardPage persistence effect's deps were deliberately modified in Step 9.2; eslint's `react-hooks/exhaustive-deps` rule should stay silent.

- [ ] **Step 10.4: Start dev server and smoke-test manually**

Backend must be running (Cerberus-managed `torque-api` on `:8990` — see boot prompt's fast health probe).

```bash
cd apps/gui
npm run dev
```

Open `http://localhost:5173/operations` and verify:

1. **Layout is two-row** — search input on top, chip row below.
2. **Search** — type a few characters; results update after ~250ms debounce. Status/Priority/etc. filters still apply on top of search (backend applies both).
3. **Search summary** — "N filters · M matches" renders correctly; switches to just "M matches" when filters are at defaults; hides entirely with no filters and empty search.
4. **`/` shortcut** — with focus anywhere on the page (but not already in an input), press `/`. Search input takes focus.
5. **`Esc` in search** — clears the input and blurs it.
6. **Clear button** — appears only when something is non-default; clicking zeros filters and search, triggers one refetch.
7. **Manual cycle button** — click cycles Both → Auto → Manual → Both. The dot color changes and the API request respects the value.
8. **Combobox pills** — click opens a typeahead popover. Select an item and the trigger pill shows the name. Select "All projects" to clear. Icon renders correctly.
9. **No Mode preset row** — gone entirely.
10. **localStorage persistence** — make a few filter + search selections, navigate to a task detail, navigate back via the browser back button. All selections restored (filters from URL, search from storage).
11. **Detail pages smoke** — navigate to a Sprint or Project detail page. Filter bar there still renders single-row with NO search field and NO Mode preset row.

If any item fails, open a bug and fix before proceeding.

- [ ] **Step 10.5: Commit changelog entry (optional)**

If the project maintains a changelog or session-wrap notes, record the feature landing in the current session notes directory:

```bash
# From agent-workspaces (tracking_root)
# Add a terse note to the session's wrap-up file if it exists.
```

If no changelog convention is in use, skip this step.

- [ ] **Step 10.6: Final push (if policy allows)**

Do NOT push without user confirmation. The feature is ready for review; ask the user whether to open a PR or merge locally per the torque profile (branch + local-merge workflow — see boot prompt pitfall #1).

---

## Self-review checklist (post-plan)

After completing all tasks, verify each design-spec requirement maps to a landed task:

| Spec requirement | Task |
|---|---|
| Two-row layout on Operations; single-row elsewhere | 8, 9 |
| Search input with `/` shortcut, `Esc` clear, 250ms debounce | 7 |
| Search combines with other filters via `api.listTasks({search})` | 9 |
| Manual cycle button (Both → Auto → Manual → Both) | 3, 4 |
| Group selectors as typeahead Comboboxes, no `__all__` placeholder | 5, 6 |
| Mode preset removed | 1 (state) + 8 (UI) |
| Storage schema v2: drop mode, add search, rename `'all'` → `'both'` | 1 |
| Summary rules: `N filters · M matches` with the four cases | 8 (shell) + 9 (counts) |
| Clear button clears filters + search together | 9 |
| Detail pages unaffected | 8 (additive props) + 9 (only BoardPage threads search) |
| Backend untouched | verified by no backend-file edits |

## Out of scope (per spec)

- Cmd+K command palette (separate feature).
- Saved views UI or storage.
- Search on detail pages.
- Backend search changes (FTS, ID matching, tag matching).
- URL sync of `search`.
