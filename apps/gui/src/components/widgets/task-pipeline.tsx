import { useMemo } from 'react'
import { BarMeter, type BarMeterRow } from '@hollis-labs/sysop-ui/widgets'
import { STATUS_COLOR_VAR, STATUS_LABEL } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

export interface TaskPipelineProps {
  tasks: Task[]
  statuses?: readonly TaskStatus[]
  className?: string
  title?: string
}

const DEFAULT_STATUSES: readonly TaskStatus[] = ['todo', 'doing', 'blocked', 'done']

/**
 * Task-pipeline meter — a thin wrapper over the kit's BarMeter. Torque counts
 * tasks per status and supplies the labeled rows.
 */
export function TaskPipeline({
  tasks,
  statuses = DEFAULT_STATUSES,
  className,
  title = 'Task Pipeline',
}: TaskPipelineProps) {
  const rows = useMemo<BarMeterRow[]>(() => {
    const counts = new Map<TaskStatus, number>()
    for (const s of statuses) counts.set(s, 0)
    for (const t of tasks) {
      if (counts.has(t.status)) counts.set(t.status, (counts.get(t.status) ?? 0) + 1)
    }
    return statuses.map((status) => ({
      key: status,
      label: STATUS_LABEL[status],
      value: counts.get(status) ?? 0,
      color: STATUS_COLOR_VAR[status],
    }))
  }, [tasks, statuses])

  return <BarMeter rows={rows} title={title} className={className} />
}
