import { HourlyPulse } from '@hollis-labs/sysop-ui'
import type { Run } from '@/lib/types'

export interface Pulse24hProps {
  runs: Run[]
  className?: string
  title?: string
  /** Height of the bar area in px. Column gutters add ~4px extra. */
  height?: number
}

/**
 * 24-hour run-activity pulse — a thin wrapper over the kit's HourlyPulse,
 * keyed on each run's start time.
 */
export function Pulse24h({ runs, className, title = '24h pulse', height = 48 }: Pulse24hProps) {
  return (
    <HourlyPulse<Run>
      items={runs}
      timestamp={(r) => r.started_at}
      title={title}
      height={height}
      className={className}
    />
  )
}
