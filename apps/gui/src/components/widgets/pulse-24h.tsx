import { useMemo } from 'react'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface Pulse24hProps {
  runs: Run[]
  className?: string
  title?: string
  /** Height of the bar area in px. Column gutters add ~4px extra. */
  height?: number
}

interface HourBucket {
  hour: number
  label: string
  count: number
}

const HOUR_MS = 60 * 60 * 1000

function toHourlyBuckets(runs: Run[]): HourBucket[] {
  const now = new Date()
  // Bucket covers the last 24 whole hours including the current hour.
  const buckets: HourBucket[] = []
  for (let i = 23; i >= 0; i--) {
    const d = new Date(now.getTime() - i * HOUR_MS)
    buckets.push({
      hour: d.getHours(),
      label: `${String(d.getHours()).padStart(2, '0')}:00`,
      count: 0,
    })
  }
  const windowStart = now.getTime() - 24 * HOUR_MS
  for (const r of runs) {
    if (!r.started_at) continue
    const t = new Date(r.started_at).getTime()
    if (!Number.isFinite(t) || t < windowStart) continue
    // Distance from "now" in hours — newer ends up at index 23.
    const hoursAgo = Math.floor((now.getTime() - t) / HOUR_MS)
    const idx = 23 - hoursAgo
    if (idx < 0 || idx > 23) continue
    buckets[idx].count++
  }
  return buckets
}

function intensity(pct: number): { color: string; opacity: number } {
  if (pct <= 0) return { color: 'var(--color-status-done)', opacity: 0 }
  if (pct > 66) return { color: 'var(--color-status-done)', opacity: 1 }
  if (pct > 33) return { color: 'var(--color-status-done)', opacity: 0.75 }
  return { color: 'var(--color-status-done)', opacity: 0.4 }
}

export function Pulse24h({
  runs,
  className,
  title = '24h pulse',
  height = 48,
}: Pulse24hProps) {
  const buckets = useMemo(() => toHourlyBuckets(runs), [runs])
  const total = buckets.reduce((a, b) => a + b.count, 0)
  const max = Math.max(1, ...buckets.map((b) => b.count))

  return (
    <div className={cn('flex flex-col gap-2', className)} aria-label={title}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          {title}
        </span>
        <span className="font-mono text-[10px] text-muted-foreground/80 tabular-nums">
          {total} runs · 24h
        </span>
      </div>

      {total === 0 ? (
        <div
          className="flex items-center justify-center rounded-sm border border-border/60 bg-muted/20 font-mono text-[10px] text-muted-foreground"
          style={{ height: height + 16 }}
        >
          No activity in last 24h
        </div>
      ) : (
        <>
          <div
            className="flex items-end gap-[2px]"
            style={{ height }}
            role="img"
            aria-label={`${total} runs in last 24 hours`}
          >
            {buckets.map((b, i) => {
              const pct = (b.count / max) * 100
              const { color, opacity } = intensity(pct)
              const shown = b.count === 0 ? 0 : Math.max(8, pct)
              return (
                <div
                  key={i}
                  title={`${b.label} — ${b.count} runs`}
                  className="flex h-full flex-1 flex-col justify-end rounded-sm bg-muted/40"
                >
                  <div
                    className="w-full rounded-sm"
                    style={{
                      height: `${shown}%`,
                      backgroundColor: color,
                      opacity,
                    }}
                  />
                </div>
              )
            })}
          </div>
          <div className="flex items-center justify-between font-mono text-[8px] text-muted-foreground/60">
            <span>-24h</span>
            <span>-12h</span>
            <span>now</span>
          </div>
        </>
      )}
    </div>
  )
}
