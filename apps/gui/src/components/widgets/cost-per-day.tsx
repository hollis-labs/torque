import { useMemo } from 'react'
import {
  Area,
  AreaChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface CostPerDayProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
  title?: string
}

interface CostBucket {
  day: string
  cost: number
}

const COST_COLOR = 'var(--color-status-review)'

function toCostBuckets(runs: Run[], days: number): CostBucket[] {
  const buckets: CostBucket[] = []
  const map = new Map<string, CostBucket>()
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const key = d.toISOString().slice(0, 10)
    const b: CostBucket = { day: key.slice(5), cost: 0 }
    buckets.push(b)
    map.set(key, b)
  }
  for (const r of runs) {
    if (!r.started_at) continue
    const key = r.started_at.slice(0, 10)
    const b = map.get(key)
    if (!b) continue
    b.cost += r.cost ?? 0
  }
  return buckets
}

function formatCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(1)}k`
  if (n >= 1) return `$${n.toFixed(2)}`
  if (n > 0) return `$${n.toFixed(3)}`
  return '$0'
}

export function CostPerDay({
  runs,
  days = 14,
  className,
  height = 140,
  title = 'Cost / day',
}: CostPerDayProps) {
  const data = useMemo(() => toCostBuckets(runs, days), [runs, days])
  const total = useMemo(() => data.reduce((acc, b) => acc + b.cost, 0), [data])

  return (
    <div className={cn('flex flex-col gap-2', className)} aria-label={title}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          {title} — last {days}d
        </span>
        <span className="font-mono text-[10px] text-muted-foreground/80 tabular-nums">
          {formatCost(total)} total
        </span>
      </div>

      {total === 0 ? (
        <div
          className="flex items-center justify-center rounded-sm border border-border/60 bg-muted/20 font-mono text-[10px] text-muted-foreground"
          style={{ height }}
        >
          No cost recorded
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={height}>
          <AreaChart data={data} margin={{ top: 6, right: 0, left: 0, bottom: 0 }}>
            <defs>
              <linearGradient id="costFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={COST_COLOR} stopOpacity={0.55} />
                <stop offset="100%" stopColor={COST_COLOR} stopOpacity={0.05} />
              </linearGradient>
            </defs>
            <XAxis
              dataKey="day"
              tick={{ fill: 'var(--muted-foreground)', fontSize: 9, fontFamily: 'ui-monospace' }}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              width={36}
              tick={{ fill: 'var(--muted-foreground)', fontSize: 9, fontFamily: 'ui-monospace' }}
              axisLine={false}
              tickLine={false}
              tickFormatter={(v: number) => formatCost(v)}
            />
            <Tooltip
              cursor={{ stroke: 'var(--border)', strokeDasharray: '2 2' }}
              contentStyle={{
                background: 'var(--popover)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                fontSize: 11,
                fontFamily: 'ui-monospace',
                color: 'var(--popover-foreground)',
              }}
              formatter={(v) =>
                formatCost(Number(Array.isArray(v) ? v[0] : v ?? 0))
              }
            />
            <Area
              type="monotone"
              dataKey="cost"
              stroke={COST_COLOR}
              strokeWidth={1.5}
              fill="url(#costFill)"
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
