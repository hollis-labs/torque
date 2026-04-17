import { useMemo } from 'react'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/types'

export interface RunStatusDistributionProps {
  runs: Run[]
  className?: string
  title?: string
  /** Diameter of the donut in px. */
  size?: number
}

type StatusBucket = 'success' | 'error' | 'active' | 'other'

interface Slice {
  key: StatusBucket
  label: string
  color: string
  count: number
}

const BUCKET_META: { key: StatusBucket; label: string; color: string }[] = [
  { key: 'success', label: 'success', color: 'var(--color-status-done)' },
  { key: 'error', label: 'error', color: 'var(--color-status-blocked)' },
  { key: 'active', label: 'active', color: 'var(--color-status-doing)' },
  { key: 'other', label: 'other', color: 'var(--color-status-backlog)' },
]

function classify(status: string): StatusBucket {
  const s = (status || '').toLowerCase()
  if (s === 'success' || s === 'done' || s === 'completed') return 'success'
  if (s === 'error' || s === 'failed' || s === 'cancelled' || s === 'canceled' || s === 'timeout')
    return 'error'
  if (s === 'running' || s === 'doing' || s === 'started' || s === 'pending' || s === 'queued')
    return 'active'
  return 'other'
}

export function RunStatusDistribution({
  runs,
  className,
  title = 'Run status',
  size = 120,
}: RunStatusDistributionProps) {
  const slices = useMemo<Slice[]>(() => {
    const counts: Record<StatusBucket, number> = {
      success: 0,
      error: 0,
      active: 0,
      other: 0,
    }
    for (const r of runs) counts[classify(r.status)]++
    return BUCKET_META.map((m) => ({ ...m, count: counts[m.key] }))
  }, [runs])

  const total = slices.reduce((acc, s) => acc + s.count, 0)
  const radius = size / 2
  const stroke = Math.max(10, Math.round(size * 0.18))
  const innerRadius = radius - stroke
  const circumference = 2 * Math.PI * (radius - stroke / 2)

  const arcs = slices
    .filter((s) => s.count > 0)
    .reduce<
      { acc: { key: StatusBucket; color: string; len: number; dashOffset: number }[]; offset: number }
    >(
      (state, s) => {
        const frac = total === 0 ? 0 : s.count / total
        const len = frac * circumference
        state.acc.push({ key: s.key, color: s.color, len, dashOffset: -state.offset })
        state.offset += len
        return state
      },
      { acc: [], offset: 0 }
    ).acc

  return (
    <div className={cn('flex flex-col gap-3', className)} aria-label={title}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
          {title}
        </span>
        <span className="font-mono text-[10px] text-muted-foreground/80 tabular-nums">
          {total} runs
        </span>
      </div>

      {total === 0 ? (
        <div
          className="flex items-center justify-center rounded-sm border border-border/60 bg-muted/20 font-mono text-[10px] text-muted-foreground"
          style={{ height: size + 4 }}
        >
          No runs recorded
        </div>
      ) : (
        <div className="flex items-center gap-4">
          <div className="relative shrink-0" style={{ width: size, height: size }}>
            <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
              <circle
                cx={radius}
                cy={radius}
                r={radius - stroke / 2}
                fill="none"
                stroke="var(--muted)"
                strokeOpacity={0.4}
                strokeWidth={stroke}
              />
              {arcs.map((a) => (
                <circle
                  key={a.key}
                  cx={radius}
                  cy={radius}
                  r={radius - stroke / 2}
                  fill="none"
                  stroke={a.color}
                  strokeWidth={stroke}
                  strokeDasharray={`${a.len} ${circumference - a.len}`}
                  strokeDashoffset={a.dashOffset}
                  transform={`rotate(-90 ${radius} ${radius})`}
                  strokeLinecap="butt"
                />
              ))}
            </svg>
            <div
              className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center"
              style={{ paddingInline: innerRadius / 8 }}
            >
              <span className="font-mono text-[16px] font-medium tabular-nums text-foreground">
                {total}
              </span>
              <span className="font-mono text-[8px] uppercase tracking-[0.2em] text-muted-foreground/70">
                runs
              </span>
            </div>
          </div>

          <ul className="flex min-w-0 flex-1 flex-col gap-1">
            {slices.map((s) => {
              const pct = total === 0 ? 0 : Math.round((s.count / total) * 100)
              return (
                <li
                  key={s.key}
                  className="flex items-center justify-between gap-2 font-mono text-[10px]"
                >
                  <span className="inline-flex items-center gap-2 uppercase tracking-widest">
                    <span
                      className="inline-block h-2 w-2 rounded-sm"
                      style={{ backgroundColor: s.color }}
                      aria-hidden
                    />
                    <span style={{ color: s.color }}>{s.label}</span>
                  </span>
                  <span className="tabular-nums text-muted-foreground">
                    {s.count} · {pct}%
                  </span>
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
