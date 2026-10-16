import { useState } from 'react'
import { completedAt, finalScope, finitePoints, linePath, scale, scopeAt, stepPath } from './math'
import { LABEL, Legend, Placeholder } from './frame'
import type { BurnData } from './types'

export interface BurndownProps {
  data: BurnData | null | undefined
  /** burndown plots remaining work; burnup plots completed work against the scope line. */
  mode?: 'burndown' | 'burnup'
  label?: string
  now?: number
  width?: number
  height?: number
  loading?: boolean
}

const PAD = { l: 28, r: 8, t: 8, b: 18 }

/** Ideal vs actual with scope-change markers. Plain SVG; scales to container width via viewBox. */
export function Burndown({ data, mode = 'burndown', label, now: nowProp, width = 480, height = 180, loading }: BurndownProps) {
  const [mounted] = useState(() => Date.now())
  const now = nowProp ?? mounted
  const title = label ?? (mode === 'burndown' ? 'Burndown' : 'Burnup')
  if (loading) return <Placeholder kind="loading" label={title} height={height} />
  if (!data || !(data.total > 0 || (data.scopeChanges?.length ?? 0) > 0) || !(data.endTs > data.startTs)) {
    return <Placeholder kind="empty" label={title} message="No scope to chart" height={height} />
  }

  const { startTs, endTs } = data
  const maxScope = Math.max(data.total, finalScope(data), ...(data.scopeChanges ?? []).map((m) => scopeAt(data, m.t)), 1)
  const x = scale(startTs, endTs, PAD.l, width - PAD.r)
  const y = scale(0, maxScope, height - PAD.b, PAD.t)
  const clampT = (t: number) => Math.min(Math.max(t, startTs), endTs)
  const cutoff = clampT(now)

  const done = finitePoints(data.completed).filter((p) => p.t >= startTs && p.t <= cutoff)
  const times = [startTs, ...done.map((p) => p.t), cutoff]
  const actualVal = (t: number) => (mode === 'burndown' ? scopeAt(data, t) - completedAt(data, t) : completedAt(data, t))
  const actual = [...new Set(times)].sort((a, b) => a - b).map((t) => [x(t), y(actualVal(t))] as const)

  const idealStart = mode === 'burndown' ? data.total : 0
  const idealEnd = mode === 'burndown' ? 0 : finalScope(data)
  const ideal = [[x(startTs), y(idealStart)], [x(endTs), y(idealEnd)]] as const

  const changes = (data.scopeChanges ?? []).filter((m) => m.t >= startTs && m.t <= endTs)
  const scopeSteps = [startTs, ...changes.map((m) => m.t), endTs].map((t) => [x(t), y(scopeAt(data, t))] as const)

  const left = actual[actual.length - 1]
  const remaining = scopeAt(data, cutoff) - completedAt(data, cutoff)
  const summary = `${title}: ${completedAt(data, cutoff)} of ${scopeAt(data, cutoff)} done, ${remaining} remaining, ${changes.length} scope ${changes.length === 1 ? 'change' : 'changes'}.`
  const ticks = [0, Math.round(maxScope / 2), maxScope]
  const fmtDate = (t: number) => new Date(t).toISOString().slice(5, 10)

  return (
    <figure className="w-full">
      <figcaption className="mb-1.5 flex items-baseline justify-between">
        <span className={LABEL}>{title}</span>
        <span className="font-mono text-xs text-zinc-300">{remaining} remaining</span>
      </figcaption>
      <svg viewBox={`0 0 ${width} ${height}`} className="h-auto w-full" role="img" aria-label={summary}>
        {ticks.map((v) => (
          <g key={v}>
            <line x1={PAD.l} x2={width - PAD.r} y1={y(v)} y2={y(v)} className="stroke-zinc-800" />
            <text x={PAD.l - 4} y={y(v) + 3} textAnchor="end" className="fill-zinc-500 font-mono text-[9px]">{v}</text>
          </g>
        ))}
        <text x={PAD.l} y={height - 4} className="fill-zinc-500 font-mono text-[9px]">{fmtDate(startTs)}</text>
        <text x={width - PAD.r} y={height - 4} textAnchor="end" className="fill-zinc-500 font-mono text-[9px]">{fmtDate(endTs)}</text>
        {mode === 'burnup' && <path d={stepPath(scopeSteps)} fill="none" strokeWidth={1.25} className="stroke-amber-300" />}
        <path d={linePath(ideal)} fill="none" strokeWidth={1} strokeDasharray="4 4" className="stroke-zinc-500" />
        {changes.map((m) => (
          <g key={`${m.t}-${m.label}`}>
            <line x1={x(m.t)} x2={x(m.t)} y1={PAD.t} y2={height - PAD.b} strokeDasharray="1 3" className="stroke-amber-400/70" />
            <title>{`${m.label} (${m.delta > 0 ? '+' : ''}${m.delta})`}</title>
            <rect x={x(m.t) - 2.5} y={PAD.t - 2} width={5} height={5} className="fill-amber-400" />
          </g>
        ))}
        {actual.length > 1 ? (
          <path d={linePath(actual)} fill="none" strokeWidth={2} strokeLinejoin="round" className="stroke-emerald-400" />
        ) : null}
        <rect x={left[0] - 2.5} y={left[1] - 2.5} width={5} height={5} className="fill-emerald-400" />
      </svg>
      <div className="mt-2">
        <Legend>
          <li className="inline-flex items-center gap-1.5"><span className="h-0.5 w-4 bg-emerald-400" aria-hidden />Actual</li>
          <li className="inline-flex items-center gap-1.5"><span className="h-0.5 w-4 border-t border-dashed border-zinc-400" aria-hidden />Ideal</li>
          {mode === 'burnup' && <li className="inline-flex items-center gap-1.5"><span className="h-0.5 w-4 bg-amber-300" aria-hidden />Scope</li>}
          {changes.length > 0 && <li className="inline-flex items-center gap-1.5"><span className="h-2 w-2 bg-amber-400" aria-hidden />Scope change</li>}
        </Legend>
      </div>
    </figure>
  )
}
