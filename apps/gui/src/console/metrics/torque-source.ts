// TorqueMetricsSource: MetricsSource backed by the existing read-only Torque REST
// endpoints (task rollup + task list + sprint/epic get). Scope strings:
//   "epic:<id>" | "sprint:<id>" | "project:<id>" | "tag:<slug>"
// Torque records no completion timestamps or scope-change history, so burn data is
// DERIVED (done task updated_at ~ completion, created_at after start ~ scope added)
// and flagged in `notes`. See BACKEND_NEEDS.md for the exact asks.
import { rollupFromStatusCounts } from '@/lib/scope-metrics'
import type { Epic, ListPage, Sprint, TaskFilter, TaskScopeKey, TaskScopeRollupResponse, TaskSummary } from '@/lib/types'
import type { BurnData, MetricPoint, MetricsSource, ProgressSegment, ProjectMetrics, TileMetric } from './types'

const DAY = 86_400_000

export type MetricScopeKind = 'epic' | 'sprint' | 'project' | 'tag'
export interface ParsedScope { kind: MetricScopeKind; id: string }

export function parseScope(scope?: string): ParsedScope | null {
  const m = /^(epic|sprint|project|tag):(.+)$/.exec(scope ?? '')
  return m ? { kind: m[1] as MetricScopeKind, id: m[2] } : null
}

/** The subset of TorqueApiClient the source reads. Read-only by construction. */
export interface TorqueMetricsClient {
  taskRollup(groupBy: TaskScopeKey, filter?: Omit<TaskFilter, 'limit' | 'offset' | 'cursor' | 'include_total' | 'sort_by' | 'sort_dir'>): Promise<TaskScopeRollupResponse>
  listTaskSummaries(filter?: TaskFilter, signal?: AbortSignal): Promise<ListPage<TaskSummary>>
  getSprint(id: string): Promise<Sprint>
  getEpic(id: string): Promise<Epic>
}

export interface TorqueMetricsOptions {
  /** Used when Torque cannot be reached (or the scope is unparseable). Results are marked origin "fixture" with a note. */
  fallback?: MetricsSource
  now?: () => number
  pageSize?: number
  maxPages?: number
}

export function progressFromCounts(counts: Readonly<Record<string, number>>): ProgressSegment[] {
  const r = rollupFromStatusCounts(counts)
  const segs: ProgressSegment[] = [
    { status: 'done', count: r.done },
    { status: 'review', count: r.review },
    { status: 'in_progress', count: r.doing },
    { status: 'blocked', count: r.blocked },
    { status: 'todo', count: r.open + r.paused },
  ]
  return segs.filter((s) => s.count > 0)
}

const EXCLUDED = new Set(['archived', 'abandoned', 'cancelled'])
const ts = (s: string | null | undefined): number => {
  const n = s ? Date.parse(s) : NaN
  return Number.isFinite(n) ? n : NaN
}

function dayBucket(t: number): number {
  return Math.floor(t / DAY) * DAY
}

/** Cumulative completion + derived scope changes from task timestamps. Exported for tests. */
export function deriveBurn(tasks: readonly Pick<TaskSummary, 'status' | 'created_at' | 'updated_at'>[], startTs: number, endTs: number): BurnData {
  const live = tasks.filter((t) => !EXCLUDED.has(t.status))
  const base = live.filter((t) => !(ts(t.created_at) > startTs))
  const added = new Map<number, number>()
  for (const t of live) {
    const c = ts(t.created_at)
    if (c > startTs) added.set(dayBucket(c), (added.get(dayBucket(c)) ?? 0) + 1)
  }
  const done = live
    .filter((t) => t.status === 'done')
    .map((t) => Math.max(startTs, ts(t.updated_at)))
    .filter(Number.isFinite)
    .sort((a, b) => a - b)
  const completed: MetricPoint[] = [{ t: startTs, v: 0 }, ...done.map((t, i) => ({ t, v: i + 1 }))]
  return {
    startTs,
    endTs,
    total: base.length,
    completed,
    scopeChanges: [...added.entries()].sort((a, b) => a[0] - b[0]).map(([t, n]) => ({ t: Math.max(t, startTs + 1), label: `${n} task${n === 1 ? '' : 's'} added`, delta: n })),
  }
}

export class TorqueMetricsSource implements MetricsSource {
  private readonly client: TorqueMetricsClient
  private readonly opts: TorqueMetricsOptions
  constructor(client: TorqueMetricsClient, opts: TorqueMetricsOptions = {}) {
    this.client = client
    this.opts = opts
  }

  async load(scope?: string, signal?: AbortSignal): Promise<ProjectMetrics> {
    const parsed = parseScope(scope)
    if (!parsed) return this.fall(scope, signal, new Error(scope ? `Unknown metrics scope "${scope}"` : 'No metrics scope given'))
    try {
      return await this.fromTorque(parsed, signal)
    } catch (e) {
      if (signal?.aborted) throw e
      return this.fall(scope, signal, e)
    }
  }

  private async fall(scope: string | undefined, signal: AbortSignal | undefined, cause: unknown): Promise<ProjectMetrics> {
    if (!this.opts.fallback) throw cause instanceof Error ? cause : new Error(String(cause))
    const m = await this.opts.fallback.load(scope, signal)
    const why = cause instanceof Error ? cause.message : String(cause)
    return { ...m, origin: 'fixture', notes: [`Showing fixture data. Torque unavailable: ${why}`, ...(m.notes ?? [])] }
  }

  private async fromTorque(p: ParsedScope, signal?: AbortSignal): Promise<ProjectMetrics> {
    const now = (this.opts.now ?? Date.now)()
    const notes: string[] = []
    const filter: TaskFilter =
      p.kind === 'epic' ? { epic_id: p.id } : p.kind === 'sprint' ? { sprint_id: p.id } : p.kind === 'project' ? { project_id: p.id } : { tags: [p.id] }

    const [counts, container, tasks] = await Promise.all([
      this.counts(p, filter),
      p.kind === 'sprint' ? this.client.getSprint(p.id) : p.kind === 'epic' ? this.client.getEpic(p.id) : Promise.resolve(null),
      this.tasks(filter, signal),
    ])
    const progress = progressFromCounts(counts ?? tallyStatuses(tasks.items))
    if (counts == null) notes.push('Progress counted from the task list (rollup unavailable for this scope).')
    if (tasks.truncated) notes.push(`Burn derived from the first ${tasks.items.length} tasks only.`)

    const sprint = p.kind === 'sprint' ? (container as Sprint) : null
    const createdTimes = tasks.items.map((t) => ts(t.created_at)).filter(Number.isFinite)
    const startTs =
      ts(sprint?.started_at) || (container ? ts((container as Epic | Sprint).created_at) : NaN) || (createdTimes.length ? Math.min(...createdTimes) : now)
    let endTs = ts(sprint?.ended_at)
    if (!(endTs > startTs)) {
      endTs = Math.max(now + DAY, startTs + 14 * DAY)
      notes.push(p.kind === 'sprint' ? 'Sprint has no end date; chart window is a 14 day default.' : 'No end date for this scope; chart window is a default.')
    }

    const live = tasks.items.filter((t) => !EXCLUDED.has(t.status))
    const burn: BurnData = deriveBurn(tasks.items, startTs, endTs)
    let burnQuality: ProjectMetrics['burnQuality'] = 'derived'
    if (live.length === 0) {
      burnQuality = 'unavailable'
    } else {
      notes.push('Burn is approximate: Torque records no completion times (task last-update is used) and no scope-change history (tasks created after the start count as added; removals are invisible).')
    }
    if (sprint && sprint.started_at == null) notes.push('Sprint has not started; chart starts at its creation time.')

    return { progress, burn, tiles: tiles(live, now), breakdown: [], origin: 'torque', burnQuality, notes }
  }

  /** Rollup counts for the scope, or null where the endpoint cannot group by it (tags). */
  private async counts(p: ParsedScope, filter: TaskFilter): Promise<Record<string, number> | null> {
    if (p.kind === 'tag') return null
    const key: TaskScopeKey = p.kind === 'epic' ? 'epic_id' : p.kind === 'sprint' ? 'sprint_id' : 'project_id'
    const res = await this.client.taskRollup(key, filter)
    return res.scopes.find((s) => s.scope_id === p.id)?.counts ?? {}
  }

  private async tasks(filter: TaskFilter, signal?: AbortSignal): Promise<{ items: TaskSummary[]; truncated: boolean }> {
    const limit = this.opts.pageSize ?? 200
    const max = this.opts.maxPages ?? 5
    const items: TaskSummary[] = []
    let cursor: string | undefined
    for (let i = 0; i < max; i++) {
      const page = await this.client.listTaskSummaries({ ...filter, limit, cursor }, signal)
      items.push(...page.items)
      if (!page.meta.has_more || !page.meta.next_cursor) return { items, truncated: false }
      cursor = page.meta.next_cursor
    }
    return { items, truncated: true }
  }
}

function tallyStatuses(items: readonly Pick<TaskSummary, 'status'>[]): Record<string, number> {
  const c: Record<string, number> = {}
  for (const t of items) c[t.status] = (c[t.status] ?? 0) + 1
  return c
}

function tiles(live: readonly TaskSummary[], now: number): TileMetric[] {
  const doneAt = live.filter((t) => t.status === 'done').map((t) => ts(t.updated_at)).filter(Number.isFinite)
  const week = 7 * DAY
  const inWin = (from: number, to: number) => doneAt.filter((t) => t > from && t <= to).length
  const daily: MetricPoint[] = []
  for (let i = 13; i >= 0; i--) daily.push({ t: dayBucket(now) - i * DAY, v: doneAt.filter((t) => dayBucket(t) === dayBucket(now) - i * DAY).length })
  const n = (s: string) => live.filter((t) => t.status === s).length
  return [
    { id: 'tasks_done_7d', label: 'done 7d', value: inWin(now - week, now), previous: inWin(now - 2 * week, now - week), series: { id: 'tasks_done_7d', label: 'done per day', points: daily } },
    { id: 'in_progress', label: 'in progress', value: n('doing') },
    { id: 'in_review', label: 'in review', value: n('review') },
    { id: 'blocked', label: 'blocked', value: n('blocked'), invert: true },
  ]
}
