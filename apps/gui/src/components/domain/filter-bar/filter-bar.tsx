import type { ReactNode } from 'react'
import { Folder, Calendar, BookOpen, Hash } from 'lucide-react'
import { FilterEntityCombobox } from './filter-entity-combobox'
import { STATUS_COLORS, DEFAULT_STATUS_COLOR, MODE_PRESETS, PRIORITIES, TASK_STATUSES } from '@/lib/constants'
import type { ManualFilter } from '@/lib/ops-filters-storage'
import type { Epic, Project, Sprint, Tag, TaskStatus } from '@/lib/types'
import { FilterCycleToggle, type CycleOption } from './filter-cycle-toggle'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const MANUAL_CYCLE_OPTIONS: ReadonlyArray<CycleOption<ManualFilter>> = [
  { value: 'both', label: 'Both', dotColor: 'bg-zinc-400', title: 'All tasks (no manual filter)' },
  { value: 'auto', label: 'Auto', dotColor: 'bg-blue-400', title: 'Scheduler-eligible (manual=false)' },
  { value: 'manual', label: 'Manual', dotColor: 'bg-amber-400', title: 'Held for review (manual=true)' },
]

interface FilterBarProps {
  activeStatuses: TaskStatus[]
  onStatusToggle: (status: TaskStatus) => void
  mode?: ModePreset
  onModeChange?: (mode: ModePreset) => void
  /** Available statuses to show — defaults to all TASK_STATUSES */
  availableStatuses?: readonly TaskStatus[]
  /** Show priority filter chips */
  activePriorities?: number[]
  onPriorityToggle?: (priority: number) => void
  /** Manual-flag filter tri-state (All / Auto / Manual). Omit to hide the toggle. */
  manualFilter?: ManualFilter
  onManualFilterChange?: (value: ManualFilter) => void
  /** Project / Sprint / Epic group selectors (all three are optional — omit to hide) */
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
  /** Extra controls rendered trailing the filter bar (e.g. Clear filters) */
  children?: ReactNode
}

export function FilterBar({
  activeStatuses,
  onStatusToggle,
  mode,
  onModeChange,
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
  children,
}: FilterBarProps) {
  const showGroups = Boolean(onProjectChange || onSprintChange || onEpicChange || onTagChange)

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5 text-xs">
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

      {/* Mode presets — only rendered when the caller passes the handler.
          (To be removed entirely in Task 8 once callers stop passing it.) */}
      {onModeChange && (
        <div className="flex items-center gap-1 border-l border-zinc-800 pl-3">
          <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Mode:</span>
          {(Object.keys(MODE_PRESETS) as Array<keyof typeof MODE_PRESETS>).map((preset) => (
            <button
              key={preset}
              type="button"
              onClick={() => onModeChange(preset)}
              className={`rounded border px-2 py-0.5 text-[10px] capitalize tracking-wider transition-all ${
                mode === preset
                  ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
                  : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
              }`}
            >
              {preset}
            </button>
          ))}
          <button
            type="button"
            onClick={() => onModeChange('all')}
            className={`rounded border px-2 py-0.5 text-[10px] tracking-wider transition-all ${
              mode === 'all'
                ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
                : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
            }`}
          >
            All
          </button>
        </div>
      )}

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

      {/* Group selectors: Project / Sprint / Epic */}
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

      {children && <div className="ml-auto flex items-center gap-2">{children}</div>}
    </div>
  )
}
