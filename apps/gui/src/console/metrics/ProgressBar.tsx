import { useId, useState } from 'react'
import { estimateEta, formatEta, segmentShares, totalCount } from './math'
import { labelForStatus, TONE, toneForStatus } from './tone'
import { LABEL, Legend, Placeholder } from './frame'
import type { ProgressSegment } from './types'

export interface ProgressBarProps {
  segments: readonly ProgressSegment[]
  label?: string
  loading?: boolean
  /** Enables the ETA hint (throughput since startTs). */
  startTs?: number
  now?: number
  showLegend?: boolean
}

/** Segmented-by-status progress with an optional ETA hint. */
export function ProgressBar({ segments, label = 'Progress', loading, startTs, now: nowProp, showLegend = true }: ProgressBarProps) {
  const id = useId()
  const [mounted] = useState(() => Date.now())
  const now = nowProp ?? mounted
  if (loading) return <Placeholder kind="loading" label={label} height={28} />
  const total = totalCount(segments)
  if (total === 0) return <Placeholder kind="empty" label={label} message="No tasks" height={28} />

  const done = segments.filter((s) => s.status === 'done').reduce((n, s) => n + s.count, 0)
  const shares = segmentShares(segments)
  const donePct = Math.round((done / total) * 100)
  const eta = startTs != null ? formatEta(estimateEta(done, total - done, startTs, now), now) : null
  const summary = segments.filter((s) => s.count > 0).map((s) => `${s.count} ${s.label ?? labelForStatus(s.status)}`).join(', ')

  return (
    <div className="w-full">
      <div className="mb-1.5 flex items-baseline justify-between gap-3">
        <span id={id} className={LABEL}>{label}</span>
        <span className="font-mono text-xs text-zinc-300">
          {done}/{total} · {donePct}%{eta ? <span className="ml-2 text-zinc-500">{eta}</span> : null}
        </span>
      </div>
      <div
        role="progressbar"
        aria-labelledby={id}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
        aria-valuetext={`${done} of ${total} done. ${summary}`}
        className="flex h-2 w-full gap-px bg-zinc-900"
      >
        {segments.map((s, i) =>
          s.count > 0 ? (
            <span key={`${s.status}-${i}`} className={TONE[toneForStatus(s.status)].bg} style={{ width: `${shares[i]}%`, minWidth: 2 }} title={`${s.label ?? labelForStatus(s.status)}: ${s.count}`} />
          ) : null,
        )}
      </div>
      {showLegend && (
        <div className="mt-2">
          <Legend>
            {segments.filter((s) => s.count > 0).map((s, i) => (
              <li key={`${s.status}-${i}`} className="inline-flex items-center gap-1.5">
                <span className={`h-2 w-2 ${TONE[toneForStatus(s.status)].bg}`} aria-hidden />
                {s.label ?? labelForStatus(s.status)} {s.count}
              </li>
            ))}
          </Legend>
        </div>
      )}
    </div>
  )
}
