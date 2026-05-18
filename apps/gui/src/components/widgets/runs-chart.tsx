import { TimeSeriesChart } from '@hollis-labs/sysop-ui'
import type { Run } from '@/lib/types'

export interface RunsChartProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
}

const SUCCESS_COLOR = 'var(--color-status-done)'
const ERROR_COLOR = 'var(--color-status-blocked)'
const RUNNING_COLOR = 'var(--color-status-doing)'

function bucket(status: string): 'success' | 'error' | 'running' {
  const s = (status || '').toLowerCase()
  if (s === 'success' || s === 'done') return 'success'
  if (s === 'error' || s === 'failed') return 'error'
  return 'running'
}

/** Stacked runs-per-day bar chart — a thin wrapper over the kit's TimeSeriesChart. */
export function RunsChart({ runs, days = 14, className, height = 160 }: RunsChartProps) {
  return (
    <TimeSeriesChart<Run>
      items={runs}
      date={(r) => r.started_at}
      kind="bar"
      days={days}
      height={height}
      title="Runs / day"
      emptyLabel="No runs in window"
      className={className}
      series={[
        {
          key: 'success',
          label: 'success',
          color: SUCCESS_COLOR,
          value: (r) => (bucket(r.status) === 'success' ? 1 : 0),
        },
        {
          key: 'error',
          label: 'error',
          color: ERROR_COLOR,
          value: (r) => (bucket(r.status) === 'error' ? 1 : 0),
        },
        {
          key: 'running',
          label: 'running',
          color: RUNNING_COLOR,
          value: (r) => (bucket(r.status) === 'running' ? 1 : 0),
        },
      ]}
    />
  )
}
