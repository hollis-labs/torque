import type { BurnData, Tone, MetricPoint, MetricSeries, ProgressSegment, TickEvent } from './types'

export function finitePoints(points: readonly MetricPoint[]): MetricPoint[] {
  return points.filter((p) => Number.isFinite(p.t) && Number.isFinite(p.v)).sort((a, b) => a.t - b.t)
}

/** Linear scale; a zero-width domain maps to the middle of the range (single-point series). */
export function scale(d0: number, d1: number, r0: number, r1: number): (v: number) => number {
  if (d1 === d0) return () => (r0 + r1) / 2
  return (v) => r0 + ((v - d0) / (d1 - d0)) * (r1 - r0)
}

export function linePath(coords: readonly (readonly [number, number])[]): string {
  return coords.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(2)} ${y.toFixed(2)}`).join(' ')
}

/** Step path: holds the previous value until the next point (for scope lines). */
export function stepPath(coords: readonly (readonly [number, number])[]): string {
  let d = ''
  coords.forEach(([x, y], i) => {
    d += i === 0 ? `M${x.toFixed(2)} ${y.toFixed(2)}` : ` H${x.toFixed(2)} V${y.toFixed(2)}`
  })
  return d
}

export function pct(part: number, whole: number): number {
  if (!(whole > 0) || !Number.isFinite(part)) return 0
  return Math.max(0, Math.min(100, (part / whole) * 100))
}

export function totalCount(segments: readonly ProgressSegment[]): number {
  return segments.reduce((n, s) => n + (Number.isFinite(s.count) && s.count > 0 ? s.count : 0), 0)
}

/** Rounded whole-number percentages that always sum to 100 (largest remainder). */
export function segmentShares(segments: readonly ProgressSegment[]): number[] {
  const total = totalCount(segments)
  if (total === 0) return segments.map(() => 0)
  const raw = segments.map((s) => (Math.max(0, s.count) / total) * 100)
  const floors = raw.map(Math.floor)
  let left = 100 - floors.reduce((a, b) => a + b, 0)
  const order = raw.map((r, i) => [r - floors[i], i] as const).sort((a, b) => b[0] - a[0])
  for (const [, i] of order) {
    if (left <= 0) break
    floors[i] += 1
    left -= 1
  }
  return floors
}

/** ETA from throughput so far: remaining / (done per ms). Null when it cannot be estimated. */
export function estimateEta(done: number, remaining: number, startTs: number, now: number): number | null {
  if (remaining <= 0) return now
  if (!(done > 0) || !(now > startTs)) return null
  const rate = done / (now - startTs)
  return now + remaining / rate
}

export function formatEta(etaTs: number | null, now: number): string {
  if (etaTs == null) return 'ETA unknown'
  const ms = etaTs - now
  if (ms <= 0) return 'complete'
  const h = ms / 3_600_000
  if (h < 1) return `ETA ~${Math.max(1, Math.round(ms / 60_000))}m`
  if (h < 48) return `ETA ~${Math.round(h)}h`
  return `ETA ~${Math.round(h / 24)}d`
}

export function scopeAt(burn: BurnData, t: number): number {
  let s = burn.total
  for (const m of burn.scopeChanges ?? []) if (m.t <= t) s += m.delta
  return s
}

export function completedAt(burn: BurnData, t: number): number {
  let v = 0
  for (const p of finitePoints(burn.completed)) {
    if (p.t <= t) v = p.v
    else break
  }
  return v
}

export function finalScope(burn: BurnData): number {
  return scopeAt(burn, Number.POSITIVE_INFINITY)
}

/** Delta between current and previous value; pct is null when previous is 0/missing. */
export function delta(value: number, previous?: number): { abs: number; pct: number | null } | null {
  if (previous == null || !Number.isFinite(previous) || !Number.isFinite(value)) return null
  return { abs: value - previous, pct: previous === 0 ? null : ((value - previous) / Math.abs(previous)) * 100 }
}

export function formatNumber(n: number, unit?: string): string {
  if (!Number.isFinite(n)) return '-'
  const abs = Math.abs(n)
  const s = abs >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : abs >= 1e4 ? `${(n / 1e3).toFixed(1)}k` : Number.isInteger(n) ? String(n) : n.toFixed(1)
  return unit ? `${s}${unit === '%' ? '' : ' '}${unit}` : s
}

/** Folds metric.tick events (payload {metric, value, unit?}) for one metric into a series. */
export function seriesFromTicks(events: readonly TickEvent[], metric: string, label = metric): MetricSeries {
  const points: MetricPoint[] = []
  let unit: string | undefined
  for (const e of events) {
    const p = e.payload as { metric?: unknown; value?: unknown; unit?: unknown } | undefined
    if (!p || p.metric !== metric || typeof p.value !== 'number') continue
    if (typeof p.unit === 'string') unit = p.unit
    points.push({ t: e.ts, v: p.value })
  }
  return { id: metric, label, unit, points: finitePoints(points) }
}

export function deltaTone(abs: number, invert?: boolean): Tone {
  if (abs === 0) return 'neutral'
  return (abs > 0) !== !!invert ? 'good' : 'bad'
}
