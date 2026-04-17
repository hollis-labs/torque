import { useMemo } from 'react'
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface RunsChartProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
}

interface Bucket {
  day: string
  success: number
  error: number
  running: number
}

const SUCCESS_COLOR = 'var(--color-status-done)'
const ERROR_COLOR = 'var(--color-status-blocked)'
const RUNNING_COLOR = 'var(--color-status-doing)'

function toBuckets(runs: Run[], days: number): Bucket[] {
  const buckets: Bucket[] = []
  const map = new Map<string, Bucket>()
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const key = d.toISOString().slice(0, 10)
    const bucket: Bucket = { day: key.slice(5), success: 0, error: 0, running: 0 }
    buckets.push(bucket)
    map.set(key, bucket)
  }
  for (const r of runs) {
    if (!r.started_at) continue
    const key = r.started_at.slice(0, 10)
    const bucket = map.get(key)
    if (!bucket) continue
    const s = (r.status || '').toLowerCase()
    if (s === 'success' || s === 'done') bucket.success++
    else if (s === 'error' || s === 'failed') bucket.error++
    else bucket.running++
  }
  return buckets
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1 font-mono text-[9px] text-muted-foreground">
      <span
        className="inline-block h-2 w-2 rounded-sm"
        style={{ backgroundColor: color }}
        aria-hidden
      />
      {label}
    </span>
  )
}

export function RunsChart({ runs, days = 14, className, height = 160 }: RunsChartProps) {
  const data = useMemo(() => toBuckets(runs, days), [runs, days])
  const total = useMemo(
    () => data.reduce((acc, b) => acc + b.success + b.error + b.running, 0),
    [data]
  )

  return (
    <div className={cn('flex flex-col gap-2', className)} aria-label="Runs per day">
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          Runs / day — last {days}d
        </span>
        <div className="flex items-center gap-3">
          <LegendDot color={SUCCESS_COLOR} label="success" />
          <LegendDot color={ERROR_COLOR} label="error" />
          <LegendDot color={RUNNING_COLOR} label="running" />
        </div>
      </div>

      {total === 0 ? (
        <div
          className="flex items-center justify-center rounded-sm border border-border/60 bg-muted/20 font-mono text-[10px] text-muted-foreground"
          style={{ height }}
        >
          No runs in window
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={height}>
          <BarChart data={data} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
            <XAxis
              dataKey="day"
              tick={{ fill: 'var(--muted-foreground)', fontSize: 9, fontFamily: 'ui-monospace' }}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              width={20}
              tick={{ fill: 'var(--muted-foreground)', fontSize: 9, fontFamily: 'ui-monospace' }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            <Tooltip
              cursor={{ fill: 'rgba(255,255,255,0.03)' }}
              contentStyle={{
                background: 'var(--popover)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                fontSize: 11,
                fontFamily: 'ui-monospace',
                color: 'var(--popover-foreground)',
              }}
            />
            <Bar dataKey="success" stackId="runs" fill={SUCCESS_COLOR} radius={[0, 0, 0, 0]} />
            <Bar dataKey="error" stackId="runs" fill={ERROR_COLOR} radius={[0, 0, 0, 0]} />
            <Bar dataKey="running" stackId="runs" fill={RUNNING_COLOR} radius={[2, 2, 0, 0]} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
