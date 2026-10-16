// Fleet metrics folded from console events: working/idle/waiting agent counts over
// time (agent.state) plus tokens/cost series when the stream carries them (metric.tick).
// Event kinds and metric names are placeholders until the data team's stream design
// lands; override through FleetOptions rather than editing call sites.
import { KINDS, type ConsoleEvent } from '../events/types'
import { finitePoints, seriesFromTicks } from './math'
import type { MetricPoint, MetricSeries } from './types'

export type FleetState = 'working' | 'idle' | 'waiting'
export const FLEET_STATES: readonly FleetState[] = ['working', 'idle', 'waiting']

export interface FleetOptions {
  now: number
  windowMs?: number
  buckets?: number
  stateKind?: string
  tickKind?: string
  tokenMetric?: string
  costMetric?: string
}

export interface FleetMetrics {
  counts: Record<FleetState, number>
  total: number
  series: Record<FleetState, MetricSeries>
  tokens: MetricSeries | null
  cost: MetricSeries | null
  /** False when the window holds no agent state events at all. */
  hasData: boolean
  notes: string[]
}

const STATE_MAP: Record<string, FleetState> = { busy: 'working', working: 'working', idle: 'idle', waiting: 'waiting' }

function agentKey(e: ConsoleEvent): string | null {
  return e.subjects.find((s) => s.type === 'agent')?.id ?? e.subject?.id ?? e.actor?.id ?? null
}

export function fleetMetricsFromEvents(events: readonly ConsoleEvent[], o: FleetOptions): FleetMetrics {
  const windowMs = o.windowMs ?? 3_600_000
  const n = Math.max(2, o.buckets ?? 24)
  const stateKind = o.stateKind ?? KINDS.agentState
  const tickKind = o.tickKind ?? KINDS.metricTick
  const states = events
    .filter((e) => e.kind === stateKind)
    .map((e) => ({ t: e.ts, agent: agentKey(e), state: STATE_MAP[String((e.payload as { state?: unknown }).state)] }))
    .filter((x): x is { t: number; agent: string; state: FleetState } => !!x.agent && !!x.state && Number.isFinite(x.t))
    .sort((a, b) => a.t - b.t)

  const at = (t: number): Record<FleetState, number> => {
    const last = new Map<string, FleetState>()
    for (const s of states) {
      if (s.t > t) break
      last.set(s.agent, s.state)
    }
    const c: Record<FleetState, number> = { working: 0, idle: 0, waiting: 0 }
    for (const v of last.values()) c[v] += 1
    return c
  }
  const pts: Record<FleetState, MetricPoint[]> = { working: [], idle: [], waiting: [] }
  for (let i = 0; i < n; i++) {
    const t = o.now - windowMs + (windowMs * i) / (n - 1)
    const c = at(t)
    for (const k of FLEET_STATES) pts[k].push({ t, v: c[k] })
  }
  const counts = at(o.now)
  const ticks = events.filter((e) => e.kind === tickKind && e.ts >= o.now - windowMs)
  const maybe = (metric: string, label: string): MetricSeries | null => {
    const s = seriesFromTicks(ticks, metric, label)
    return s.points.length > 0 ? s : null
  }
  const tokens = maybe(o.tokenMetric ?? 'tokens_per_min', 'tokens / min')
  const cost = maybe(o.costMetric ?? 'cost', 'cost')
  const notes: string[] = []
  if (!tokens) notes.push('No token ticks in the stream yet.')
  if (!cost) notes.push('No cost ticks in the stream yet.')
  return {
    counts,
    total: counts.working + counts.idle + counts.waiting,
    series: {
      working: { id: 'working', label: 'working', points: finitePoints(pts.working) },
      idle: { id: 'idle', label: 'idle', points: finitePoints(pts.idle) },
      waiting: { id: 'waiting', label: 'waiting', points: finitePoints(pts.waiting) },
    },
    tokens,
    cost,
    hasData: states.length > 0,
    notes,
  }
}
