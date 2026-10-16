import type { BurnData, MetricPoint, MetricSeries, MetricsSource, ProjectMetrics, ProgressSegment } from './types'

const HOUR = 3_600_000
const DAY = 24 * HOUR

function rng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function fixtureSeries(id: string, opts: { seed?: number; n?: number; end?: number; stepMs?: number; base?: number; drift?: number; noise?: number; unit?: string } = {}): MetricSeries {
  const { seed = 1, n = 24, end = Date.parse('2026-10-16T12:00:00Z'), stepMs = HOUR, base = 50, drift = 0, noise = 10, unit } = opts
  const r = rng(seed)
  const points: MetricPoint[] = []
  for (let i = 0; i < n; i++) points.push({ t: end - (n - 1 - i) * stepMs, v: Math.max(0, Math.round(base + drift * i + (r() - 0.5) * noise)) })
  return { id, label: id.replace(/_/g, ' '), unit, points }
}

export function fixtureProgress(variant: 'mixed' | 'empty' | 'done' | 'single' = 'mixed'): ProgressSegment[] {
  switch (variant) {
    case 'empty': return []
    case 'done': return [{ status: 'done', count: 12 }]
    case 'single': return [{ status: 'todo', count: 1 }]
    default: return [
      { status: 'done', count: 9 }, { status: 'review', count: 3 }, { status: 'in_progress', count: 4 },
      { status: 'blocked', count: 1 }, { status: 'todo', count: 7 },
    ]
  }
}

/** A two-week sprint, 20 tasks, two scope changes, a stall mid-way. */
export function fixtureBurn(now = Date.parse('2026-10-16T12:00:00Z')): BurnData {
  const startTs = now - 9 * DAY
  const completions = [0.5, 1, 1.2, 2, 2.1, 3.5, 4, 5, 5.2, 7, 7.5, 8.5]
  return {
    startTs,
    endTs: startTs + 14 * DAY,
    total: 20,
    completed: [{ t: startTs, v: 0 }, ...completions.map((d, i) => ({ t: startTs + d * DAY, v: i + 1 }))],
    scopeChanges: [
      { t: startTs + 3 * DAY, label: 'Added CI follow-ups', delta: 3 },
      { t: startTs + 6 * DAY, label: 'Deferred docs pass', delta: -2 },
    ],
  }
}

export function fixtureMetrics(seed = 1): ProjectMetrics {
  const now = Date.parse('2026-10-16T12:00:00Z')
  const tile = (id: string, value: number, previous: number, extra: { unit?: string; invert?: boolean; drift?: number; base?: number } = {}) => ({
    id, label: id.replace(/_/g, ' '), value, previous, unit: extra.unit, invert: extra.invert,
    series: fixtureSeries(id, { seed: seed + id.length, base: extra.base ?? value, drift: extra.drift ?? 0, unit: extra.unit, end: now }),
  })
  return {
    progress: fixtureProgress('mixed'),
    burn: fixtureBurn(now),
    tiles: [
      tile('tasks_done', 12, 9, { base: 6, drift: 0.3 }),
      tile('active_agents', 3, 3, { base: 3, drift: 0 }),
      tile('failed_runs', 4, 2, { invert: true, base: 2, drift: 0.1 }),
      tile('tokens_per_min', 8200, 9100, { unit: 'tok', base: 8500, drift: -30 }),
    ],
    breakdown: [
      { status: 'done', count: 41, label: 'Succeeded' }, { status: 'blocked', count: 4, label: 'Failed' },
      { status: 'review', count: 3, label: 'Cancelled' }, { status: 'in_progress', count: 2, label: 'Running' },
    ],
  }
}

export interface FixtureMetricsOptions {
  delayMs?: number
  seed?: number
  /** Return empty data (zero tasks) to exercise degenerate states. */
  empty?: boolean
  fail?: string
}

/** Deterministic MetricsSource for previews and tests. P2 replaces it with Torque-backed data. */
export class FixtureMetricsSource implements MetricsSource {
  private readonly opts: FixtureMetricsOptions
  constructor(opts: FixtureMetricsOptions = {}) {
    this.opts = opts
  }

  async load(_scope?: string, signal?: AbortSignal): Promise<ProjectMetrics> {
    if (this.opts.delayMs) {
      await new Promise<void>((resolve, reject) => {
        const t = setTimeout(resolve, this.opts.delayMs)
        signal?.addEventListener('abort', () => { clearTimeout(t); reject(new DOMException('aborted', 'AbortError')) })
      })
    }
    if (this.opts.fail) throw new Error(this.opts.fail)
    if (this.opts.empty) {
      const now = Date.parse('2026-10-16T12:00:00Z')
      return { progress: [], burn: { startTs: now, endTs: now + DAY, total: 0, completed: [] }, tiles: [], breakdown: [] }
    }
    return fixtureMetrics(this.opts.seed)
  }
}
