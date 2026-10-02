import { ScopePicker } from '../scope-picker'
import { useState, type ComponentProps, type ReactNode } from 'react'
import { Hash, SlidersHorizontal, Plus, Zap } from 'lucide-react'
import { FilterEntityCombobox, FilterSearchInput } from '@hollis-labs/sysop-ui/data'
import { STATUS_COLORS, DEFAULT_STATUS_COLOR, PRIORITIES, TASK_STATUSES } from '@/lib/constants'
import type { ManualFilter } from '@/lib/ops-filters-storage'
import type { Tag, TaskStatus } from '@/lib/types'

function CompactEntityFilter({ onCreate, createLabel, onMore, loadingMore, ...props }: ComponentProps<typeof FilterEntityCombobox> & { onMore?: () => void; loadingMore?: boolean }) {
  return <div className="inline-flex items-stretch [&>button]:rounded-r-none">
    <FilterEntityCombobox {...props} />
    {onMore && <button type="button" disabled={loadingMore} onClick={onMore} aria-label={`Load more ${props.allLabel.toLowerCase()}`}
      className="h-7 border border-l-0 border-border-subtle bg-panel-2/50 px-1.5 text-[10px] text-text-soft">More</button>}
    {onCreate && <button type="button" onClick={onCreate} aria-label={createLabel ?? 'New item'}
      title={createLabel} className="inline-flex h-7 items-center gap-1 rounded-r border border-l-0 border-border-subtle bg-panel-2/50 px-1.5 text-[10px] text-text-soft">
      <Plus className="h-3 w-3" /> New
    </button>}
  </div>
}

interface FilterBarProps {
  eligibleOnly?: boolean
  onEligibleChange?: (enabled: boolean) => void
  editorControls?: ReactNode
  activeViewName?: string
  activeStatuses: TaskStatus[]
  onStatusToggle: (status: TaskStatus) => void
  /** Available statuses to show — defaults to all TASK_STATUSES */
  availableStatuses?: readonly TaskStatus[]
  /** Show priority filter chips */
  activePriorities?: number[]
  onPriorityToggle?: (priority: number) => void
  /** Manual and Auto toggles; both enabled or both disabled mean no restriction. Omit to hide the control. */
  manualFilter?: ManualFilter
  onManualFilterChange?: (value: ManualFilter) => void
  /**
   * System (kind=internal) toggle. When provided, renders a chip that
   * controls whether kind=internal automation tasks (Reviewer end-agents
   * etc.) are surfaced in the list. Default off (CW-20260503-0011).
   */
  includeInternal?: boolean
  onIncludeInternalChange?: (value: boolean) => void
  /** Project / Sprint / Epic / Tag combobox selectors (all optional) */
  projectId?: string | null
  onProjectChange?: (id: string | null) => void
  sprintId?: string | null
  onSprintChange?: (id: string | null) => void
  onSprintCreate?: () => void
  epicId?: string | null
  onEpicChange?: (id: string | null) => void
  tags?: Tag[]
  tagSlug?: string | null
  onTagChange?: (slug: string | null) => void
  onProjectCreate?: () => void
  onEpicCreate?: () => void
  onTagCreate?: () => void

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

export function FilterBar({
  eligibleOnly = false,
  onEligibleChange,
  editorControls,
  activeViewName,
  activeStatuses,
  onStatusToggle,
  availableStatuses = TASK_STATUSES,
  activePriorities = [],
  onPriorityToggle,
  manualFilter,
  onManualFilterChange,
  includeInternal = false,
  onIncludeInternalChange,
  projectId,
  onProjectChange,
  sprintId,
  onSprintChange,
  onSprintCreate,
  epicId,
  onEpicChange,
  tags,
  tagSlug,
  onTagChange,
  onProjectCreate,
  onEpicCreate,
  onTagCreate,
  searchQuery,
  onSearchChange,
  searchMatchCount,
  activeFilterCount = 0,
  onClear,
  children,
}: FilterBarProps) {
  const [editorOpen, setEditorOpen] = useState(false)
  const showGroups = Boolean(onProjectChange || onSprintChange || onEpicChange || onTagChange)
  const twoRow = searchQuery !== undefined && onSearchChange !== undefined

  const chipRow = (
    <div className="flex flex-wrap items-center gap-3 text-xs">
      {/* Status chips */}
      <div className="flex flex-wrap items-center gap-1">
        <span className="mr-1 text-[10px] uppercase tracking-wider text-text-subtle">Status:</span>
        {availableStatuses.map((status) => {
          const active = activeStatuses.includes(status)
          const colors = STATUS_COLORS[status] ?? DEFAULT_STATUS_COLOR
          return (
            <button
              key={status}
              type="button"
              aria-pressed={active}
              onClick={() => onStatusToggle(status)}
              className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
                active
                  ? `${colors.border} ${colors.bg} ${colors.text} ring-1 ring-white/20`
                  : 'border-border bg-panel-2/50 text-text-subtle opacity-50 hover:text-text-soft'
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
          <span className="mr-1 text-[10px] uppercase tracking-wider text-text-subtle">Priority:</span>
          {PRIORITIES.map(({ value, label }) => {
            const active = activePriorities.includes(value)
            return (
              <button
                key={value}
                type="button"
                aria-pressed={active}
                onClick={() => onPriorityToggle(value)}
                className={`rounded border px-2 py-0.5 text-[10px] font-medium tracking-wider transition-all ${
                  active
                    ? 'border-status-paused/40 bg-status-paused/10 text-status-paused'
                    : 'border-border bg-panel-2/50 text-text-subtle hover:text-text-soft'
                }`}
              >
                {label}
              </button>
            )
          })}
        </div>
      )}

      {onManualFilterChange && <div className="flex items-center gap-1 border-l border-border pl-3">
        {(['manual', 'auto'] as const).map(value => {
          const on = manualFilter === 'both' || manualFilter === undefined || manualFilter === value
          const other = value === 'manual' ? 'auto' : 'manual'
          const otherOn = manualFilter === 'both' || manualFilter === undefined || manualFilter === other
          return <button key={value} type="button" aria-pressed={on}
            onClick={() => onManualFilterChange(on ? (otherOn ? other : 'both') : 'both')}
            className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider ${on ? 'border-status-doing/40 bg-status-doing/10 text-text-soft' : 'border-border text-text-subtle opacity-50'}`}>
            {value === 'manual' ? 'Manual' : 'Auto'}
          </button>
        })}
      </div>}

      {/* System toggle (kind=internal) — CW-20260503-0011 */}
      {onIncludeInternalChange && (
        <div className="flex items-center gap-1 border-l border-border pl-3">
          <span className="mr-1 text-[10px] uppercase tracking-wider text-text-subtle">System:</span>
          <button
            type="button"
            onClick={() => onIncludeInternalChange(!includeInternal)}
            aria-pressed={includeInternal}
            title={
              includeInternal
                ? 'Hide kind=internal automation tasks (Reviewer end-agents, etc.)'
                : 'Show kind=internal automation tasks (Reviewer end-agents, etc.)'
            }
            className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
              includeInternal
                ? 'border-status-review/40 bg-status-review/10 text-status-review ring-1 ring-white/20'
                : 'border-border bg-panel-2/50 text-text-subtle hover:text-text-soft'
            }`}
          >
            {includeInternal ? 'Shown' : 'Hidden'}
          </button>
        </div>
      )}

      {/* Group combobox pills */}
      {showGroups && (
        <div className="flex flex-wrap items-center gap-2 border-l border-border pl-3">
          {onProjectChange && (
            <div className="inline-flex items-center gap-1"><ScopePicker kind="project" value={projectId} onChange={onProjectChange} placeholder="Projects" label="Filter by project" className="h-7 w-40 justify-between text-xs" />{onProjectCreate && <button type="button" aria-label="New project" onClick={onProjectCreate} className="h-7 rounded border border-border px-2 text-xs"><Plus className="size-3" /></button>}</div>
          )}
          {onEpicChange && (
            <div className="inline-flex items-center gap-1"><ScopePicker kind="epic" value={epicId} onChange={onEpicChange} projectId={projectId ?? undefined} placeholder="Epics" label="Filter by epic" className="h-7 w-40 justify-between text-xs" />{onEpicCreate && <button type="button" aria-label="New epic" onClick={onEpicCreate} className="h-7 rounded border border-border px-2 text-xs"><Plus className="size-3" /></button>}</div>
          )}
          {onSprintChange && (
            <div className="inline-flex items-center gap-1"><ScopePicker kind="sprint" value={sprintId} onChange={onSprintChange} projectId={projectId ?? undefined} placeholder="Sprints" label="Filter by sprint" className="h-7 w-40 justify-between text-xs" />{onSprintCreate && <button type="button" aria-label="New sprint" onClick={onSprintCreate} className="h-7 rounded border border-border px-2 text-xs"><Plus className="size-3" /></button>}</div>
          )}
          {onTagChange && (
            <CompactEntityFilter
              icon={<Hash className="h-3.5 w-3.5" />}
              items={(tags ?? []).map((t) => ({ id: t.slug, name: t.name }))}
              value={tagSlug ?? null}
              onChange={onTagChange}
              allLabel="Tags"
              ariaLabel="Filter by tag"
              onCreate={onTagCreate}
              createLabel="New tag"
            />
          )}
        </div>
      )}
    </div>
  )

  if (twoRow) {
    const hasSearch = !eligibleOnly && (searchQuery ?? '').length > 0
    const showClear = Boolean(onClear) && (activeFilterCount > 0 || hasSearch)
    const showSummary = activeFilterCount > 0 || hasSearch

    let summaryText = ''
    if (activeFilterCount > 0 && hasSearch) {
      summaryText = `${activeFilterCount} filter${activeFilterCount === 1 ? '' : 's'} · ${searchMatchCount ?? '…'} match${searchMatchCount === 1 ? '' : 'es'}`
    } else if (activeFilterCount > 0) {
      summaryText = `${activeFilterCount} filter${activeFilterCount === 1 ? '' : 's'}`
    } else if (hasSearch) {
      summaryText = `${searchMatchCount ?? '…'} match${searchMatchCount === 1 ? '' : 'es'}`
    }

    return (
      <div className="flex flex-col border-b border-border bg-panel">
        {/* Row 1: search hero + summary + clear */}
        <div className="flex items-center gap-3 px-4 py-2">
          {onEligibleChange && <button type="button" aria-pressed={eligibleOnly} onClick={() => onEligibleChange(!eligibleOnly)}
            title="Static task eligibility: Todo automatic tasks with allowed kinds, required profile selectors, and all dependencies done. Runtime capacity, project availability, and profile readiness are not evaluated."
            className={`inline-flex h-8 items-center gap-1 rounded border px-2 text-xs ${eligibleOnly ? 'border-status-doing bg-status-doing/10 text-status-doing' : 'border-border text-text-soft'}`}>
            <Zap className="h-3.5 w-3.5" /> Eligible
          </button>}
          <fieldset disabled={eligibleOnly} className={`min-w-0 flex-1 ${eligibleOnly ? 'opacity-40' : ''}`}>
            <FilterSearchInput value={searchQuery ?? ''} onChange={onSearchChange!} debounceMs={0} />
          </fieldset>
          <div className="inline-flex h-8 items-center gap-1.5 rounded border border-border bg-panel-2/50 px-2 text-[10px] uppercase tracking-wider text-text-soft">
            <SlidersHorizontal className="h-3.5 w-3.5" />
            {activeFilterCount}
          </div>
          {showSummary && (
            <span className="whitespace-nowrap text-[10px] uppercase tracking-wider text-text-subtle">
              {summaryText}
            </span>
          )}
          {activeViewName && <span className="max-w-40 truncate text-xs text-text-soft">{activeViewName}</span>}
          <button type="button" aria-label="Edit filters and saved views" aria-expanded={editorOpen}
            onClick={() => setEditorOpen(!editorOpen)} className="rounded border border-border p-2 text-text-soft">
            <SlidersHorizontal className="h-3.5 w-3.5" />
          </button>
          {showClear && (
            <button
              type="button"
              onClick={onClear}
              aria-label="Clear all filters and search"
              className="rounded border border-border-strong bg-transparent px-2 py-1 text-[10px] uppercase tracking-wider text-text-muted transition-colors hover:border-border-strong hover:text-text"
            >
              Clear
            </button>
          )}
        </div>
        {/* Row 2: compact chip row */}
        <fieldset disabled={eligibleOnly} className={`border-t border-border px-4 py-2 ${eligibleOnly ? 'opacity-40' : ''}`}>{chipRow}</fieldset>
        {eligibleOnly && <p className="px-4 pb-2 text-xs text-text-subtle">Static eligibility; the scheduler may still skip tasks for runtime reasons.</p>}
        {editorOpen && editorControls && <div className="border-t border-border px-4 py-3">{editorControls}</div>}
      </div>
    )
  }

  // Single-row (legacy) layout for detail pages.
  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-border bg-panel px-4 py-2.5">
      {chipRow}
      {children && <div className="ml-auto flex items-center gap-2">{children}</div>}
    </div>
  )
}
