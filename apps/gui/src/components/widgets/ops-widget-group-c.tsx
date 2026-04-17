import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'
import { Pulse24h } from './pulse-24h'
import { CostPerDay } from './cost-per-day'

export interface OpsWidgetGroupCProps {
  runs: Run[]
  costDays?: number
  className?: string
}

export function OpsWidgetGroupC({
  runs,
  costDays = 14,
  className,
}: OpsWidgetGroupCProps) {
  return (
    <div
      className={cn(
        'grid grid-cols-1 gap-4 lg:grid-cols-2',
        className
      )}
    >
      <Card size="sm">
        <CardContent>
          <Pulse24h runs={runs} />
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <CostPerDay runs={runs} days={costDays} />
        </CardContent>
      </Card>
    </div>
  )
}
