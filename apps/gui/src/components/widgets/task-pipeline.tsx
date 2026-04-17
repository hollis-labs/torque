import { useMemo } from 'react'
import { cn } from '@/lib/utils'
import { STATUS_COLOR_VAR, STATUS_LABEL } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

export interface TaskPipelineProps {
  tasks: Task[]
  statuses?: readonly TaskStatus[]
  className?: string
  title?: string
}

const DEFAULT_STATUSES: readonly TaskStatus[] = ['todo', 'doing', 'blocked', 'done']

export function TaskPipeline({
  tasks,
  statuses = DEFAULT_STATUSES,
  className,
  title = 'Task Pipeline',
}: TaskPipelineProps) {
  const counts = useMemo(() => {
    const c = new Map<TaskStatus, number>()
    for (const s of statuses) c.set(s, 0)
    for (const t of tasks) {
      if (c.has(t.status)) c.set(t.status, (c.get(t.status) ?? 0) + 1)
    }
    return c
  }, [tasks, statuses])

  const max = Math.max(1, ...Array.from(counts.values()))
  const total = Array.from(counts.values()).reduce((a, b) => a + b, 0)

  return (
    <div className={cn('flex flex-col gap-2', className)} aria-label={title}>
      <div className="mb-1 flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          {title}
        </span>
        <span className="font-mono text-[10px] text-muted-foreground/80 tabular-nums">
          {total} total
        </span>
      </div>
      {statuses.map((status) => {
        const count = counts.get(status) ?? 0
        const pct = (count / max) * 100
        const color = STATUS_COLOR_VAR[status]
        return (
          <div key={status}>
            <div className="mb-1 flex items-center justify-between">
              <span
                className="font-mono text-[9px] uppercase tracking-widest"
                style={{ color }}
              >
                {STATUS_LABEL[status]}
              </span>
              <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
                {count}
              </span>
            </div>
            <div className="h-[5px] w-full overflow-hidden rounded-sm bg-muted/50">
              <div
                className="h-full rounded-sm"
                style={{
                  width: `${pct}%`,
                  backgroundColor: color,
                }}
              />
            </div>
          </div>
        )
      })}
    </div>
  )
}
