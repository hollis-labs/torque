import { useMemo } from 'react'
import { TimeSeriesChart } from '@hollis-labs/sysop-ui'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface TokenThroughputProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
  title?: string
}

const PROMPT_COLOR = 'var(--color-status-doing)'
const COMPLETION_COLOR = 'var(--color-status-done)'

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return `${n}`
}

/**
 * Stacked prompt/completion-tokens-per-day chart — a thin wrapper over the
 * kit's TimeSeriesChart, with a Torque-specific running-total footer.
 */
export function TokenThroughput({
  runs,
  days = 14,
  className,
  height = 160,
  title = 'Tokens / day',
}: TokenThroughputProps) {
  const total = useMemo(
    () => runs.reduce((acc, r) => acc + (r.prompt_tokens ?? 0) + (r.completion_tokens ?? 0), 0),
    [runs],
  )

  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <TimeSeriesChart<Run>
        items={runs}
        date={(r) => r.started_at}
        kind="bar"
        days={days}
        height={height}
        title={title}
        emptyLabel="No tokens in window"
        formatValue={formatTokens}
        series={[
          { key: 'prompt', label: 'prompt', color: PROMPT_COLOR, value: (r) => r.prompt_tokens ?? 0 },
          {
            key: 'completion',
            label: 'completion',
            color: COMPLETION_COLOR,
            value: (r) => r.completion_tokens ?? 0,
          },
        ]}
      />
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] text-muted-foreground/70">
          {formatTokens(total)} total tokens
        </span>
      </div>
    </div>
  )
}
