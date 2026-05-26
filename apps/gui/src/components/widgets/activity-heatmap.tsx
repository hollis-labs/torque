import { useMemo } from 'react'
import { ActivityHeatmap as KitActivityHeatmap } from '@hollis-labs/sysop-ui/widgets'
import type { Run, Task } from '@/lib/types'

export interface ActivityHeatmapProps {
  tasks: Task[]
  runs: Run[]
  weekCount?: number
  className?: string
}

interface DatedEvent {
  date: string | null | undefined
}

/**
 * Activity heatmap — a thin wrapper over the kit's ActivityHeatmap. Torque
 * folds both task updates and run starts into a single dated-event stream.
 */
export function ActivityHeatmap({ tasks, runs, weekCount = 16, className }: ActivityHeatmapProps) {
  const items = useMemo<DatedEvent[]>(
    () => [
      ...tasks.map((t) => ({ date: t.updated_at })),
      ...runs.map((r) => ({ date: r.started_at })),
    ],
    [tasks, runs],
  )

  return (
    <KitActivityHeatmap<DatedEvent>
      items={items}
      date={(e) => e.date}
      weekCount={weekCount}
      className={className}
    />
  )
}
