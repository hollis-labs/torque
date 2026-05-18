import { useMemo } from 'react'
import { TimeSeriesChart } from '@hollis-labs/sysop-ui'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface CostPerDayProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
  title?: string
}

const COST_COLOR = 'var(--color-status-review)'

function formatCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(1)}k`
  if (n >= 1) return `$${n.toFixed(2)}`
  if (n > 0) return `$${n.toFixed(3)}`
  return '$0'
}

/**
 * Cost-per-day area chart — a thin wrapper over the kit's TimeSeriesChart
 * (area kind), with a Torque-specific running-total caption.
 */
export function CostPerDay({
  runs,
  days = 14,
  className,
  height = 140,
  title = 'Cost / day',
}: CostPerDayProps) {
  const total = useMemo(() => runs.reduce((acc, r) => acc + (r.cost ?? 0), 0), [runs])

  return (
    <div className={cn('flex flex-col gap-1', className)}>
      <div className="flex items-center justify-end">
        <span className="font-mono text-[10px] tabular-nums text-muted-foreground/80">
          {formatCost(total)} total
        </span>
      </div>
      <TimeSeriesChart<Run>
        items={runs}
        date={(r) => r.started_at}
        kind="area"
        days={days}
        height={height}
        title={title}
        emptyLabel="No cost recorded"
        formatValue={formatCost}
        series={[{ key: 'cost', label: 'cost', color: COST_COLOR, value: (r) => r.cost ?? 0 }]}
      />
    </div>
  )
}
