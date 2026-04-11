# Task Detail/Edit Page Rebuild — Design Spec

**Date:** 2026-04-09
**Project:** 3 of 3 (Tag system → Task API expansion → Task detail/edit rebuild)
**Status:** Approved

## Overview

Rebuild `TaskDetailPage` to match the tasks home HUD aesthetic (zinc-950, uppercase labels, mono font, accent bars) and add whole-page edit mode via `?edit=1` query param. Extract a shared `DetailSection` shell so Epic/Sprint/Project detail pages can adopt the HUD language in a follow-up pass.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Scope | Task-only rebuild + shared scaffolding | NavArrows and other detail page rebuilds deferred |
| Edit mode | `?edit=1` whole-page toggle | Single page, two modes. Common single-field changes (status, tags) covered by view-mode controls |
| Field scope | Tiers 1–4 editable, Tier 5 deferred | `permissions`/`metadata` JSON editors are a separate design problem |
| Layout | Full-width sections, no sidebar | Sidebar too narrow for 4 tiers of fields |
| Visual | Structured panels with colored accent bars | Each section gets identity via left border color |
| Back-link | Always `← Board` → `/` | Context-aware back-links deferred to NavArrows project |
| Architecture | Single page, conditional rendering | `editing` prop flows to section components; no react-hook-form |
| NavArrows | Deferred | Built once all four detail pages share the HUD language |
| Context-aware back-link | Deferred | Bundled with NavArrows project |

## Page Structure

```
TaskDetailPage (page owner)
├── reads ?edit=1 → derives `editing: boolean`
├── fetches task via useApi().getTask(id)
├── if editing: clones task into `draft` state, manages form mutations
│
├── TaskDetailHeader
│   ├── breadcrumb: ← Board / {id}
│   ├── title (text in view, input in edit)
│   ├── status + priority + tags badges
│   └── action buttons: transition buttons (view) | Save + Cancel (edit)
│
├── TaskProperties (accent: blue, always open)
│   └── 3×3 grid: executor, profile, working_dir, budget, retries, manual, project, sprint, epic
│
├── Description (accent: zinc, always open)
│   └── text block (view) | textarea (edit)
│
├── ExecutionContext (accent: violet, collapsible, default closed)
│   └── system_prompt, tools, files, environment, max_duration_ms, token_budget
│
├── LifecycleRules (accent: amber, collapsible, default closed)
│   └── on_done, on_fail, on_review, on_done_merge, escalation_chain, quality_gates, blocked_reason
│
├── DeliverablesAndDeps (accent: red, collapsible, default closed)
│   └── depends_on, deliverable_preset, deliverables[]
│
└── ActivityTabs (comments / runs / artifacts — hidden in edit mode)
```

## Component Design

### TaskDetailHeader

```
Props:
  task: Task
  editing: boolean
  draft: Partial<Task>
  onDraftChange: (field, value) => void
  onSave: () => void
  onCancel: () => void
  onTransition: (status: TaskStatus) => void
  saving: boolean
  children?: ReactNode          // slot for future NavArrows
```

**View mode:** breadcrumb, title (`text-base font-semibold`), badge row (StatusBadge + PriorityBadge + TagChips uncapped), right side has transition buttons (up to 4) + "Edit" button.

**Edit mode:** title becomes `<Input>`, priority becomes `<Select>` or number input, tags become chip editor (sends tag names, API auto-resolves). Right side has "Save" (primary) + "Cancel" (ghost).

### DetailSection (shared layout shell)

```
Props:
  label: string
  accent: string                // tailwind color for left border
  collapsible: boolean          // default true
  defaultOpen: boolean          // default false
  summary?: string              // inline preview when collapsed
  children: ReactNode
```

Renders: `border border-zinc-800/50 rounded-md` with `border-l-2` in the accent color. Collapsed state shows label + summary. Expanded shows label + children.

### TaskProperties (accent: blue, not collapsible)

3-column grid of label/value pairs.

| Field | View | Edit |
|-------|------|------|
| executor | text | `<Input>` |
| agent_profile | text | `<Input>` |
| working_dir | mono text | `<Input>` |
| cost_budget | `$5.00` / `unlimited` / `default` | `<Input type="number">` + unlimited toggle |
| max_retries | number | `<Input type="number">` |
| manual | Yes/No | `<Switch>` |
| project_id | linked name (blue) or "none" | `<Select>` from `api.listProjects()` |
| sprint_id | linked name (blue) or "none" | `<Select>` from `api.listSprints()` |
| epic_id | linked name (blue) or "none" | `<Select>` from `api.listEpics()` |

Container pickers lazy-load options when edit mode activates. View-mode linked names navigate to entity detail pages.

### ExecutionContext (accent: violet, collapsible)

| Field | View | Edit |
|-------|------|------|
| system_prompt | pre-wrapped text block | `<Textarea>` |
| tools | chip list | comma-separated `<Input>`, chips on blur |
| files | chip list | same as tools |
| environment | key-value table (2-col, mono) | editable rows with add/remove |
| max_duration_ms | formatted duration / `unlimited` / `default` | `<Input type="number">` + unlimited toggle |
| token_budget | formatted number / `unlimited` / `default` | `<Input type="number">` + unlimited toggle |

Sentinel display: `null` → "default" (zinc-500 italic), `-1` → "unlimited" (mono), `0` → "0" (cost_budget only), positive → formatted.

### LifecycleRules (accent: amber, collapsible)

| Field | View | Edit |
|-------|------|------|
| on_done | badge label | `<Select>`: close \| review \| notify |
| on_fail | badge label | `<Select>`: retry \| block \| escalate \| notify |
| on_review | badge label | `<Select>`: pause \| notify \| auto-approve |
| on_done_merge | badge label | `<Select>`: none \| auto \| pr \| auto-resolve |
| escalation_chain | chip list | comma-separated input |
| quality_gates | chip list | comma-separated input |
| blocked_reason | text or "—" | `<Textarea>` |

Collapsed summary shows current enum values joined: `close · retry · pause · none`.

### DeliverablesAndDeps (accent: red, collapsible)

| Field | View | Edit |
|-------|------|------|
| depends_on | list of task ID links | `<Input>` for task IDs (validated server-side) |
| deliverable_preset | text or "—" | `<Input>` |
| deliverables | table: type \| required \| description | `<Select>` for type (12 enum values), `<Switch>` for required, `<Input>` for description. Add/remove row buttons. |

Collapsed summary: `N deliverables · M deps`.

## Edit Mode Mechanics

### Form State

When `?edit=1` is set, the page clones `task` into `draft` via `useState<Partial<Task>>`. Mutations flow through:

```typescript
function updateDraft<K extends keyof Task>(field: K, value: Task[K]) {
  setDraft(prev => ({ ...prev, [field]: value }))
}
```

Section components receive `draft` and `onDraftChange` (which calls `updateDraft`).

### Save Flow

1. User clicks "Save" → `saving: true` (disables button, shows spinner)
2. Compute diff: only send changed fields to keep the PATCH minimal
3. Tags: map `Tag[]` → `string[]` (tag names) before sending
4. Call `api.updateTask(id, diff)`
5. Success: replace `task` state with response, clear `draft`, navigate to drop `?edit=1`
6. Error: show inline error banner below header, keep draft intact for retry

### Cancel Flow

1. Discard `draft` state
2. Navigate to drop `?edit=1`
3. No confirmation dialog

### SSE

Not added to the detail page in this iteration. Task is fetched once on mount and re-fetched after save. Avoids draft-clobbering. Live updates are a follow-up.

### Validation

Minimal client-side — API does the heavy lifting:
- Title can't be empty
- Numeric fields use `type="number"`
- Save button disabled while `saving` is true
- API 400s surface as inline error banner with server message

### Container Picker Loading

On edit mode activation, three parallel fetches: `listProjects()`, `listSprints()`, `listEpics()`. Cached in component state for the edit session. Failed fetches show "failed to load" and fall back to raw ID display.

## Activity Zone (Tabs)

- Tabs restyled to HUD: `text-[10px] uppercase tracking-[.18em]`, active tab has bottom border, zinc palette
- Sits below `border-t border-zinc-800/80` separator
- **Hidden in edit mode** — reappears on save/cancel
- Lazy loading per tab stays
- `CommentList`, `RunCard`, `EmptyState` components untouched
- Artifact rendering stays Card-based

## Visual Language

All elements follow the HUD aesthetic established on the tasks home page:

- Background: `bg-zinc-950`
- Labels: `text-[10px] uppercase tracking-[.18em] text-zinc-500`
- Values: `text-[13px] text-zinc-300` (or `text-zinc-100` for primary)
- Mono: `font-mono` for IDs, paths, sentinel values
- Borders: `border-zinc-800/50` or `border-zinc-800/80`
- Title: `text-base font-semibold tracking-[.02em] text-zinc-100` (down from `text-xl`)
- Accent bars: 2px left border per section (blue, violet, amber, red, zinc)
- Links to entities: `text-blue-400` (project, sprint, epic, task IDs)
- Buttons: `border border-zinc-800/80 bg-zinc-950 text-[11px] uppercase tracking-[.22em]`

## File Organization

### New files

| File | Purpose |
|------|---------|
| `components/domain/detail-section.tsx` | Shared section shell (accent bar, collapse, summary) |
| `components/domain/task-detail-header.tsx` | HUD-style header for task detail |
| `components/domain/task-properties.tsx` | Properties grid (Tier 1) |
| `components/domain/execution-context.tsx` | Execution context panel (Tier 2) |
| `components/domain/lifecycle-rules.tsx` | Lifecycle rules panel (Tier 3) |
| `components/domain/deliverables-deps.tsx` | Deliverables & dependencies panel (Tier 4) |

### Modified files

| File | Change |
|------|--------|
| `pages/TaskDetailPage.tsx` | Full rewrite |

### Untouched

- `detail-header.tsx` — stays for other detail pages
- `CommentList`, `RunCard`, `EmptyState`, `TagChip`, `StatusBadge`, `PriorityBadge`
- `App.tsx`, `lib/api.ts`, `lib/types.ts`
- All other pages

### No new dependencies

All built with existing shadcn components and Tailwind.

## Deferred Work

| Item | Deferred To |
|------|-------------|
| NavArrows component | Post all-detail-page rebuild |
| Context-aware back-link (`← Epic: foo`) | Bundled with NavArrows |
| Tier 5 fields (`permissions`, `metadata`) | Own session — structured editors, not JSON blobs |
| Inline edit conveniences (click-to-edit fields in view mode) | Post-MVP |
| SSE live updates on detail page | Follow-up |
| Other detail page HUD rebuilds (Epic, Sprint, Project) | Separate projects |
| `@mention` style task ID picker for `depends_on` | Post-MVP |
