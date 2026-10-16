import type { ConsoleEvent } from '../events/types'

/** Semantic colour roles. Components map these to theme classes; callers never pass raw colours. */
export type Tone = 'neutral' | 'good' | 'warn' | 'bad' | 'info'

export interface MetricPoint {
  /** epoch ms */
  t: number
  v: number
}

/** A scope change on a burn chart (tasks added or removed mid-flight). */
export interface MetricMarker {
  t: number
  label: string
  /** Change in total scope: positive adds work, negative removes it. */
  delta: number
}

/**
 * One named time series. Shape is additive and event-friendly: a `metric.tick`
 * ConsoleEvent (metric, value, unit) folds into a series with `seriesFromTicks`.
 */
export interface MetricSeries {
  id: string
  label: string
  unit?: string
  points: MetricPoint[]
}

export type MetricStatus = 'todo' | 'in_progress' | 'review' | 'blocked' | 'done'

export interface ProgressSegment {
  status: MetricStatus | (string & {})
  count: number
  label?: string
}

export interface BurnData {
  startTs: number
  endTs: number
  /** Scope at startTs. */
  total: number
  /** Cumulative completed count over time (sorted ascending). */
  completed: MetricPoint[]
  scopeChanges?: MetricMarker[]
}

export type MetricsLoad<T> =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; data: T }

export interface TileMetric {
  id: string
  label: string
  value: number
  unit?: string
  /** Previous-period value; the tile derives the delta from it. */
  previous?: number
  /** When true a rising value is bad (cost, failures). */
  invert?: boolean
  series?: MetricSeries
}

export interface ProjectMetrics {
  progress: ProgressSegment[]
  burn: BurnData
  tiles: TileMetric[]
  /** Breakdown for StackedBar (e.g. runs by outcome). */
  breakdown: ProgressSegment[]
  /** Where the numbers came from; absent means unspecified (older sources). */
  origin?: 'torque' | 'fixture'
  /** How trustworthy `burn` is: derived from task timestamps (approximate), or not derivable. */
  burnQuality?: 'derived' | 'unavailable'
  /** Plain-language caveats a view should show next to the data (degradations, approximations). */
  notes?: string[]
}

/**
 * Where the metrics kit gets data. P1 ships a fixture implementation; P2 swaps in
 * real Torque data. Methods are async so a network source drops in unchanged.
 */
export interface MetricsSource {
  load(scope?: string, signal?: AbortSignal): Promise<ProjectMetrics>
}

export type TickEvent = Pick<ConsoleEvent, 'ts' | 'payload'>
