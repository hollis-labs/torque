import { useMemo } from 'react'
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface TokenThroughputProps {
  runs: Run[]
  days?: number
  className?: string
  height?: number
  title?: string
}

interface TokenBucket {
  day: string
  prompt: number
  completion: number
}

const PROMPT_COLOR = 'var(--color-status-doing)'
const COMPLETION_COLOR = 'var(--color-status-done)'

function toTokenBuckets(runs: Run[], days: number): TokenBucket[] {
  const buckets: TokenBucket[] = []
  const map = new Map<string, TokenBucket>()
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const key = d.toISOString().slice(0, 10)
    const b: TokenBucket = { day: key.slice(5), prompt: 0, completion: 0 }
    buckets.push(b)
    map.set(key, b)
  }
  for (const r of runs) {
    if (!r.started_at) continue
    const key = r.started_at.slice(0, 10)
    const b = map.get(key)
    if (!b) continue
    b.prompt += r.prompt_tokens ?? 0
    b.completion += r.completion_tokens ?? 0
  }
  return buckets
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return `${n}`
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

export function TokenThroughput({
  runs,
  days = 14,
  className,
  height = 160,
  title = 'Tokens / day',
}: TokenThroughputProps) {
  const data = useMemo(() => toTokenBuckets(runs, days), [runs, days])
  const total = useMemo(
    () => data.reduce((acc, b) => acc + b.prompt + b.completion, 0),
    [data]
  )

  return (
    <div className={cn('flex flex-col gap-2', className)} aria-label={title}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          {title} — last {days}d
        </span>
        <div className="flex items-center gap-3">
          <LegendDot color={PROMPT_COLOR} label="prompt" />
          <LegendDot color={COMPLETION_COLOR} label="completion" />
        </div>
      </div>

      {total === 0 ? (
        <div
          className="flex items-center justify-center rounded-sm border border-border/60 bg-muted/20 font-mono text-[10px] text-muted-foreground"
          style={{ height }}
        >
          No tokens in window
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
              width={28}
              tick={{ fill: 'var(--muted-foreground)', fontSize: 9, fontFamily: 'ui-monospace' }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
              tickFormatter={(v: number) => formatTokens(v)}
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
              formatter={(v) =>
                formatTokens(Number(Array.isArray(v) ? v[0] : v ?? 0))
              }
            />
            <Bar dataKey="prompt" stackId="tokens" fill={PROMPT_COLOR} radius={[0, 0, 0, 0]} />
            <Bar dataKey="completion" stackId="tokens" fill={COMPLETION_COLOR} radius={[2, 2, 0, 0]} />
          </BarChart>
        </ResponsiveContainer>
      )}

      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] text-muted-foreground/70">
          {formatTokens(total)} total tokens
        </span>
      </div>
    </div>
  )
}
