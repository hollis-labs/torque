import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'
import { TokenThroughput } from './token-throughput'
import { RunStatusDistribution } from './run-status-distribution'
import { RecentRuns } from './recent-runs'

export interface OpsWidgetGroupBProps {
  runs: Run[]
  tokenDays?: number
  recentLimit?: number
  className?: string
  onSelectRun?: (run: Run) => void
}

export function OpsWidgetGroupB({
  runs,
  tokenDays = 14,
  recentLimit = 12,
  className,
  onSelectRun,
}: OpsWidgetGroupBProps) {
  return (
    <div
      className={cn(
        'grid grid-cols-1 gap-4 xl:grid-cols-[1fr_minmax(260px,320px)_minmax(260px,360px)]',
        className
      )}
    >
      <Card size="sm">
        <CardContent>
          <TokenThroughput runs={runs} days={tokenDays} />
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <RunStatusDistribution runs={runs} />
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <RecentRuns runs={runs} limit={recentLimit} onSelect={onSelectRun} />
        </CardContent>
      </Card>
    </div>
  )
}
