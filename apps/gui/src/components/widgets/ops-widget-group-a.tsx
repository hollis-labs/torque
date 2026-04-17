import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import type { Run, Task } from '@/lib/types'
import { ActivityHeatmap } from './activity-heatmap'
import { RunsChart } from './runs-chart'
import { TaskPipeline } from './task-pipeline'

export interface OpsWidgetGroupAProps {
  tasks: Task[]
  runs: Run[]
  weekCount?: number
  runsDays?: number
  className?: string
}

export function OpsWidgetGroupA({
  tasks,
  runs,
  weekCount = 16,
  runsDays = 14,
  className,
}: OpsWidgetGroupAProps) {
  return (
    <div
      className={cn(
        'grid grid-cols-1 gap-4 xl:grid-cols-[auto_1fr_260px]',
        className
      )}
    >
      <Card size="sm">
        <CardContent>
          <div className="mb-3 flex items-center justify-between gap-4">
            <span className="whitespace-nowrap font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
              Activity — {weekCount}w
            </span>
            <span className="font-mono text-[9px] text-muted-foreground/70">
              tasks · runs
            </span>
          </div>
          <ActivityHeatmap tasks={tasks} runs={runs} weekCount={weekCount} />
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <RunsChart runs={runs} days={runsDays} />
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <TaskPipeline tasks={tasks} />
        </CardContent>
      </Card>
    </div>
  )
}
