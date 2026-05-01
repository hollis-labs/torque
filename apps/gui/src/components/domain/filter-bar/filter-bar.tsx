import type { ReactNode } from 'react'
import { Folder, Calendar, BookOpen, Hash, SlidersHorizontal } from 'lucide-react'
import { FilterEntityCombobox } from './filter-entity-combobox'
import { FilterSearchInput } from './filter-search-input'
import { STATUS_COLORS, DEFAULT_STATUS_COLOR, PRIORITIES, TASK_STATUSES } from '@/lib/constants'
import type { ManualFilter } from '@/lib/ops-filters-storage'
import type { Epic, Project, Sprint, Tag, TaskStatus } from '@/lib/types'
import { FilterCycleToggle, type CycleOption } from './filter-cycle-toggle'

const MANUAL_CYCLE_OPTIONS: readonly [
  CycleOption<ManualFilter>,
  ...CycleOption<ManualFilter>[],
] = [
  { value: 'both', label: 'Both', dotColor: 'bg-zinc-400', title: 'All tasks (no manual filter)' },
  { value: 'auto', label: 'Auto', dotColor: 'bg-blue-400', title: 'Scheduler-eligible (manual=false)' },
  { value: 'manual', label: 'Manual', dotColor: 'bg-amber-400', title: 'Held for review (manual=true)' },
]

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
  onSprintCreate?: () => void
  epics?: Epic[]
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
  onSprintCreate,
  epics,
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
              showStateControls
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
              showStateControls
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
              onCreate={onSprintCreate}
              createLabel="New sprint"
              showStateControls
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
              onCreate={onTagCreate}
              createLabel="New tag"
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
          <div className="inline-flex h-8 items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[10px] uppercase tracking-wider text-zinc-400">
            <SlidersHorizontal className="h-3.5 w-3.5" />
            {activeFilterCount}
          </div>
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
