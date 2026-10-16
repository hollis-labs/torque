import { finitePoints, linePath, scale } from './math'
import { TONE } from './tone'
import type { MetricPoint, Tone } from './types'

export interface SparklineProps {
  points: readonly MetricPoint[]
  /** Required: describes the series for assistive tech. */
  label: string
  tone?: Tone
  width?: number
  height?: number
  loading?: boolean
}

/** Tiny inline trend. Single point renders a dot, no data renders a flat dashed baseline. */
export function Sparkline({ points, label, tone = 'neutral', width = 96, height = 24, loading }: SparklineProps) {
  const pts = finitePoints(points)
  const pad = 2
  const box = { width, height, viewBox: `0 0 ${width} ${height}` }
  if (loading) {
    return <svg {...box} role="img" aria-busy="true" aria-label={`${label} loading`}><rect width={width} height={height} className="animate-pulse fill-zinc-900" /></svg>
  }
  if (pts.length === 0) {
    return (
      <svg {...box} role="img" aria-label={`${label}: no data`}>
        <line x1={pad} x2={width - pad} y1={height / 2} y2={height / 2} strokeDasharray="2 3" className="stroke-zinc-700" />
      </svg>
    )
  }
  const vs = pts.map((p) => p.v)
  const x = scale(pts[0].t, pts[pts.length - 1].t, pad, width - pad)
  const y = scale(Math.min(...vs), Math.max(...vs), height - pad, pad)
  const coords = pts.map((p) => [x(p.t), y(p.v)] as const)
  const last = coords[coords.length - 1]
  return (
    <svg {...box} role="img" aria-label={`${label}: ${pts.length} points, latest ${pts[pts.length - 1].v}, min ${Math.min(...vs)}, max ${Math.max(...vs)}`}>
      {pts.length > 1 && <path d={linePath(coords)} fill="none" strokeWidth={1.5} strokeLinejoin="round" className={TONE[tone].stroke} />}
      <rect x={last[0] - 1.5} y={last[1] - 1.5} width={3} height={3} className={`${TONE[tone].stroke} fill-current ${TONE[tone].text}`} />
    </svg>
  )
}
