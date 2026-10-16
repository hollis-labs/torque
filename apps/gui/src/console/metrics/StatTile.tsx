import { delta, deltaTone, formatNumber } from './math'
import { LABEL, Placeholder } from './frame'
import { Sparkline } from './Sparkline'
import { TONE } from './tone'
import type { MetricSeries } from './types'

export interface StatTileProps {
  label: string
  value: number | null | undefined
  unit?: string
  previous?: number
  /** Rising is bad (cost, failures). */
  invert?: boolean
  series?: MetricSeries
  loading?: boolean
}

/** Label, big value, delta vs previous period, optional sparkline. Square-edged to match Operations. */
export function StatTile({ label, value, unit, previous, invert, series, loading }: StatTileProps) {
  if (loading) return <Placeholder kind="loading" label={label} height={84} />
  const hasValue = typeof value === 'number' && Number.isFinite(value)
  const d = hasValue ? delta(value, previous) : null
  const tone = d ? deltaTone(d.abs, invert) : 'neutral'
  const arrow = !d || d.abs === 0 ? '→' : d.abs > 0 ? '▲' : '▼'
  const dText = d ? `${d.abs > 0 ? '+' : ''}${formatNumber(d.abs)}${d.pct != null ? ` (${d.pct > 0 ? '+' : ''}${d.pct.toFixed(0)}%)` : ''}` : null
  return (
    <div className="flex min-w-[10rem] flex-col gap-1 border-b border-zinc-800/80 px-4 py-3" role="group" aria-label={label}>
      <span className={LABEL}>{label}</span>
      <div className="flex items-end justify-between gap-3">
        <span className="font-mono text-2xl leading-none text-zinc-100">{hasValue ? formatNumber(value, unit) : '–'}</span>
        {series && <Sparkline points={series.points} label={series.label} tone={tone} />}
      </div>
      {dText && (
        <span className={`font-mono text-[11px] ${TONE[tone].text}`}>
          <span aria-hidden>{arrow} </span>
          <span className="sr-only">{d!.abs > 0 ? 'up' : d!.abs < 0 ? 'down' : 'unchanged'} </span>
          {dText} <span className="text-zinc-600">vs prev</span>
        </span>
      )}
    </div>
  )
}
