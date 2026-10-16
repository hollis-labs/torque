import { labelForStatus, TONE, toneForStatus } from './tone'
import { LABEL, Legend, Placeholder } from './frame'
import { formatNumber, segmentShares, totalCount } from './math'
import type { ProgressSegment } from './types'

export interface StackedBarProps {
  segments: readonly ProgressSegment[]
  label?: string
  loading?: boolean
  height?: number
}

/** Proportional breakdown (e.g. runs by outcome). Same data shape as ProgressBar, no progress semantics. */
export function StackedBar({ segments, label = 'Breakdown', loading, height = 12 }: StackedBarProps) {
  if (loading) return <Placeholder kind="loading" label={label} height={height + 24} />
  const total = totalCount(segments)
  if (total === 0) return <Placeholder kind="empty" label={label} height={height + 24} />
  const shares = segmentShares(segments)
  const alt = segments.filter((s) => s.count > 0).map((s) => `${s.label ?? labelForStatus(s.status)} ${s.count}`).join(', ')
  return (
    <figure className="w-full">
      <figcaption className="mb-1.5 flex items-baseline justify-between">
        <span className={LABEL}>{label}</span>
        <span className="font-mono text-xs text-zinc-300">{formatNumber(total)}</span>
      </figcaption>
      <div role="img" aria-label={`${label}: ${alt}`} className="flex w-full gap-px bg-zinc-900" style={{ height }}>
        {segments.map((s, i) =>
          s.count > 0 ? <span key={`${s.status}-${i}`} className={TONE[toneForStatus(s.status)].bg} style={{ width: `${shares[i]}%`, minWidth: 2 }} /> : null,
        )}
      </div>
      <div className="mt-2">
        <Legend>
          {segments.filter((s) => s.count > 0).map((s, i) => (
            <li key={`${s.status}-${i}`} className="inline-flex items-center gap-1.5">
              <span className={`h-2 w-2 ${TONE[toneForStatus(s.status)].bg}`} aria-hidden />
              {s.label ?? labelForStatus(s.status)} {s.count} <span className="text-zinc-500">{shares[i]}%</span>
            </li>
          ))}
        </Legend>
      </div>
    </figure>
  )
}
