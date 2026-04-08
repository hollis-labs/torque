import { STATUS_COLORS, DEFAULT_STATUS_COLOR, MODE_PRESETS, PRIORITIES, TASK_STATUSES } from '@/lib/constants'
import type { TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

interface FilterBarProps {
  activeStatuses: TaskStatus[]
  onStatusToggle: (status: TaskStatus) => void
  mode: ModePreset
  onModeChange: (mode: ModePreset) => void
  /** Available statuses to show — defaults to all TASK_STATUSES */
  availableStatuses?: readonly TaskStatus[]
  /** Show priority filter chips */
  activePriorities?: number[]
  onPriorityToggle?: (priority: number) => void
}

export function FilterBar({
  activeStatuses,
  onStatusToggle,
  mode,
  onModeChange,
  availableStatuses = TASK_STATUSES,
  activePriorities = [],
  onPriorityToggle,
}: FilterBarProps) {
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

      {/* Mode presets */}
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
    </div>
  )
}
