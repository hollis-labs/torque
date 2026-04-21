# Tasks Filter Bar Redesign + Search

**Date:** 2026-04-21
**Status:** Design complete, pending implementation plan
**Scope:** `apps/gui` (Operations page — `BoardPage`)
**Related:** CW-20260417-0484 (FE filter bar polish), CW-20260417-0398 (manual filter tri-state, merged)

---

## Summary

The Operations filter bar has accreted controls over several sprints (Status, Priority, Mode preset, Manual tri-state, Project/Sprint/Epic/Tag selects) and now wraps and feels clunky. This design adds a first-class search input, reorganizes the bar into a two-row layout, and simplifies individual controls. Detail pages (Sprint/Project/Epic) stay on the single-row variant for now.

## Goals

- Add scoped search to the Operations task list, wired to the existing backend `search` filter param.
- Reduce visual clutter: fewer controls, denser per-control footprint, clearer hierarchy.
- Treat search as the primary action (top row, generous width); push faceted filters to a compact secondary row.
- Keep the shared `FilterBar` prop contract additive so the three detail pages are unaffected by this work.

## Non-goals

- Cmd+K global command palette (separate feature; MVP of Cmd+K is search-only; filter integration is a later task).
- Saved views (long-term goal; the filter-state shape should not block it, but no UI lands now).
- Adding search to Sprint/Project/Epic detail pages (v2 for those pages).
- Backend changes — the list endpoint already accepts `search`.

## Decisions locked

- **Layout:** two rows. Row 1 = search input (flex-grow) + right-aligned summary (`N filters · M matches` when searching, `N filters` otherwise) + `Clear` button. Row 2 = compact chips/controls.
- **Mode preset:** removed. Users click Status chips directly.
- **Manual filter:** single cycle button (order: `Both → Auto → Manual → Both`) with a colored dot (`Both` = gray, `Auto` = blue, `Manual` = amber). The stored value `'all'` is renamed to `'both'` for label honesty via a storage migration.
- **Priority:** stays as three multi-select chips (P1, P2, P3).
- **Group selectors (Project/Sprint/Epic/Tag):** shadcn `Popover` + `Command` comboboxes with icon prefix, typeahead inside the popover, and muted "All X" text when unselected. The `__all__` sentinel is gone from the user-facing rendering.
- **Search match fields:** title and description (existing backend `SearchTasks` behavior). Task ID jump-to is handled by the separate Cmd+K palette.
- **Search ↔ filters:** search is combined with all other active filters via the existing `filter.search` list-endpoint param. No new backend work.
- **Keyboard:** `/` focuses the search input when no other input is focused; `Esc` inside the input clears and blurs.
- **Persistence:** search query lives in `ops-filters-storage` alongside other filters (250ms debounce). Not mirrored to URL.

## Architecture

### New directory

```
apps/gui/src/components/domain/filter-bar/
├── filter-bar.tsx              # shell; switches between single-row and two-row modes
├── filter-search-input.tsx     # debounced search field + keyboard shortcuts
├── filter-cycle-toggle.tsx     # generic cycle button (used for Manual)
├── filter-entity-combobox.tsx  # shadcn Popover + Command combobox
├── filter-chip-row.tsx         # Row 2 composition
└── index.ts                    # re-exports FilterBar
```

The existing `apps/gui/src/components/domain/filter-bar.tsx` is deleted after the move. All four current callers import `FilterBar` — the new `index.ts` re-exports it, so imports stay stable.

### Layout selection

`FilterBar` renders the two-row layout only when both `searchQuery` and `onSearchChange` props are provided. When omitted (detail pages), it renders the existing single-row layout. This keeps the contract additive.

### Component responsibilities

**`FilterSearchInput`**
- Props: `value`, `onChange`, `placeholder?`.
- Maintains a local `string` state for the input; debounces 250ms before calling `onChange`.
- Registers a window-level `keydown` listener on mount for `/` (focuses the input when no other `<input>` / `<textarea>` / `[contenteditable]` is focused — so the shortcut is a no-op when the user is already typing in the search input).
- `Esc` clears local state and blurs.
- Match count is rendered by the summary, not by this component.

**`FilterCycleToggle<T>`**
- Props: `options: Array<{ value: T; label: string; dotColor?: string; title?: string }>`, `value: T`, `onChange: (next: T) => void`, `ariaLabel`.
- Click advances index, wrapping at the end.
- Renders as a single button with optional colored dot + label.

**`FilterEntityCombobox`**
- Props: `icon: ReactNode`, `items: Array<{ id: string; name: string }>`, `value: string | null`, `onChange: (id: string | null) => void`, `allLabel: string`, `ariaLabel: string`, `onCreate?: () => void`, `createLabel?: string`.
- Trigger: icon + selected item name (or `allLabel` in muted color) + caret.
- Popover: shadcn `Command` with typeahead; first row is always the `allLabel` option (selects `null`); remaining rows are items; optional "+ Create" footer row when `onCreate` is provided.

**`FilterChipRow`** (two-row mode only)
- Renders Status chips, Priority chips, Manual cycle, and the four comboboxes with separators.

### State shape

Extend `apps/gui/src/lib/ops-filters-storage.ts`:

```ts
export type ManualFilter = 'both' | 'auto' | 'manual'

export interface OpsFilters {
  activeStatuses: TaskStatus[]
  activePriorities: number[]
  manualFilter: ManualFilter
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  search: string                 // NEW — default ''
  // REMOVED: mode
}

export const OPS_FILTERS_STORAGE_VERSION = 2
```

### Migration

On hydration, if the stored blob:
1. Has a `version` ≠ 2 (or missing), run the migration:
   - Drop `mode` if present.
   - If `manualFilter === 'all'`, rewrite as `'both'`.
   - Ensure `search` defaults to `''`.
   - Set `version: 2`.
2. Write the migrated blob back to localStorage.

## Data flow

```
User types in FilterSearchInput
  → local state updates immediately (field stays responsive)
  → 250ms debounce
  → calls onChange(query)
    → BoardPage setState({ search: query })
      → effect writes ops-filters-storage (debounced alongside other filters)
      → effect refetches listTasks({ ..., search: query })
        → backend applies `title LIKE %q% OR description LIKE %q%` on top of other filters
      → response updates task table and matchCount
        → FilterSearchInput receives matchCount via FilterBar prop
```

Backend endpoint: no change. The existing `filter.search` pass-through in `api.ts:220` is already wired.

## UI copy

- Search placeholder: `Search tasks by title or description…`
- Clear button: `Clear` — rendered only when at least one filter is non-default OR search is non-empty
- Manual cycle labels: `Both`, `Auto`, `Manual`
- Group combobox "all" labels: `All projects`, `All sprints`, `All epics`, `All tags`

### Summary rules

`N filters` counts *filter dimensions* in a non-default state, not individual chip selections. The dimensions are: Status (non-default subset), Priority (any selection), Manual (non-`both`), Project, Sprint, Epic, Tag. Search is tracked separately.

| Filters | Search | Summary text |
|---|---|---|
| 0 | empty | *(summary hidden)* |
| N > 0 | empty | `N filters` |
| 0 | non-empty | `M matches` |
| N > 0 | non-empty | `N filters · M matches` |

## Accessibility

- Search input: `<input type="search" aria-label="Search tasks">`; `/` shortcut documented in `title` attribute.
- Cycle toggle: `role="button"` (native `<button>`), `aria-label="Manual filter, current: Auto"` with dynamic label; optionally mirror as a `<select>`-style widget if keyboard nav becomes a pain (defer).
- Combobox: shadcn `Command` handles `aria-expanded`, listbox semantics, and keyboard nav.
- Clear: `<button aria-label="Clear all filters and search">Clear</button>`.
- Focus order: search → summary (not focusable) → Clear → Row 2 controls left-to-right.

## Testing

### Unit (Vitest + React Testing Library)

- `filter-search-input.test.tsx`
  - Debounce: rapid input produces exactly one `onChange` after 250ms.
  - `/` shortcut focuses the input only when no other input/textarea/contenteditable is focused.
  - `Esc` clears local value and blurs.
  - Controlled `value` prop updates reflect in the rendered field.
- `filter-cycle-toggle.test.tsx`
  - Clicking advances through `options` and wraps.
  - Keyboard `Enter`/`Space` trigger advance.
- `filter-entity-combobox.test.tsx`
  - Typeahead narrows the visible items.
  - Selecting the "All X" row calls `onChange(null)`.
  - `onCreate` renders a footer row and fires the callback.
- `ops-filters-storage.test.ts`
  - v1 blob without `search` + with `mode` migrates cleanly.
  - `manualFilter: 'all'` rewrites to `'both'`.
  - Round-trip write/read preserves `search` and `version: 2`.

### Integration (BoardPage)

- Typing in search triggers one `listTasks` call per debounced change (not per keystroke); existing `BoardPage.rehydrate.test.tsx` pattern.
- Search combines with Status/Priority/Project in a single API call (assert params).
- `Clear` button zeros all filters + search and triggers one refetch.
- `searchMatchCount` appears in the summary only when `search` is non-empty.
- Detail pages (smoke render test): `SprintDetailPage`, `ProjectDetailPage`, `EpicDetailPage` render the single-row `FilterBar` without throwing when `searchQuery` props are omitted.

## Out of scope

- Cmd+K command palette implementation.
- Saved views UI or storage schema.
- Adding search to Sprint/Project/Epic detail pages.
- Backend search enhancements (FTS, ID matching, tag matching).
- URL sync of `search`.

## Follow-up candidates

- **Cmd+K palette** (shadcn `Command`, fixed search header, filter subheader — MVP search-only, filter integration later).
- **Detail-page search**: re-use `FilterSearchInput` + two-row layout on Sprint/Project/Epic detail pages once the Operations version is stable.
- **Saved views**: formalize `OpsFilters` as a `FilterView` model with a name + id, list UI for switching between views.
- **Richer search**: ID-prefix matching (`CW-…`), tag-prefix matching (`#`), status tokens (`status:todo`) — these may slot more naturally into Cmd+K than inline search.

## Known limitations

- Search is client-debounced but server-executed per request — no in-flight request cancellation. Fast typers on a large DB may see a brief lag between keystroke and result. Acceptable for MVP; revisit if felt.
- `/` focus shortcut collides with any future editor feature bound to `/`. Unlikely in the Operations page; document and move on.
