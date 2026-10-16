// Adapters from today's live feeds onto ConsoleSource. They depend on narrow
// structural slices of ApiClient, so tests (and later transports) can stand in.
// Mapping is lossy by design: raw upstream payloads are kept in `payload`.

import { parseFleetAgents } from '@/lib/fleet-agents'
import { parseConsoleEvent, parseGapInfo } from './parse'
import type { ConsoleSource, SnapshotOptions, SubscribeHandlers } from './source'
import { KINDS, type ConsoleEvent, type ConsoleSnapshot } from './types'

export interface ActivityApi {
  getActivityAgents(): Promise<unknown>
  subscribeActivity(onEvent: (kind: string, data: unknown) => void, onStatus: (s: 'live' | 'error') => void): () => void
}

export interface TorqueEventsApi {
  subscribeEvents(
    onEvent: (e: { type: string; data: Record<string, unknown> }) => void,
    onStatus?: (s: 'connecting' | 'live' | 'error') => void,
  ): () => void
}

/** Chimera activity-monitor relay (/api/v1/activity/*): go-runtime-events envelopes. */
export class ActivityStreamSource implements ConsoleSource {
  readonly id = 'activity'
  private readonly api: ActivityApi
  constructor(api: ActivityApi) {
    this.api = api
  }

  async snapshot(): Promise<ConsoleSnapshot> {
    const agents = parseFleetAgents(await this.api.getActivityAgents())
    const events: ConsoleEvent[] = agents.map((a) => ({
      id: `activity-snap:${a.id}:${a.state}:${a.lastActivity ?? 0}`,
      ts: a.lastActivity ?? a.startedAt ?? Date.now(),
      kind: KINDS.agentState,
      source: { id: 'activity' },
      subjects: [{ type: 'agent', id: a.id, label: a.id }],
      severity: 'info',
      payload: { state: a.state, waitingFor: a.waitingFor ?? undefined, cwd: a.cwd, provider: a.provider },
      actions: [],
      extra: {},
    }))
    return { events, cursor: null }
  }

  subscribe(h: SubscribeHandlers): () => void {
    return this.api.subscribeActivity(
      (kind, data) => {
        if (kind === 'gap') return h.onGap(parseGapInfo(data))
        const o = data && typeof data === 'object' ? (data as Record<string, unknown>) : null
        if (!o) return
        const sid = typeof o.session_id === 'string' ? o.session_id : ''
        const e = parseConsoleEvent(
          { ...o, kind: typeof o.kind === 'string' ? o.kind : kind, id: o.id ?? `${kind}:${sid}:${String(o.time ?? '')}`,
            subjects: o.subjects ?? (sid ? [{ type: 'agent', id: sid, label: sid }] : []), source: o.source ?? { id: 'activity' } },
          { source: { id: 'activity' } },
        )
        if (e) h.onEvents([e], e.cursor, e.epoch)
      },
      (s) => h.onStatus(s),
    )
  }
}

/** Torque /events SSE: domain events like task.updated. Stream-only, so the snapshot is empty. */
export class TorqueEventsSource implements ConsoleSource {
  readonly id = 'torque'
  private n = 0
  private readonly api: TorqueEventsApi
  constructor(api: TorqueEventsApi) {
    this.api = api
  }

  async snapshot(): Promise<ConsoleSnapshot> {
    return { events: [], cursor: null }
  }

  subscribe(h: SubscribeHandlers): () => void {
    return this.api.subscribeEvents(
      (ev) => {
        // The server sends a gap frame when it cannot replay from the client's
        // Last-Event-ID (pruned, other generation, cursor ahead). It is a stream
        // signal, not a domain event.
        if (ev.type === 'gap') return h.onGap(parseGapInfo(ev.data))
        const d = ev.data ?? {}
        const subjId = typeof d.id === 'string' ? d.id : typeof d.task_id === 'string' ? d.task_id : ''
        const e = parseConsoleEvent({
          id: `torque:${ev.type}:${subjId}:${Date.now()}:${this.n++}`,
          kind: `torque.${ev.type}`,
          source: { id: 'torque' },
          subjects: subjId ? [{ type: ev.type.split('.')[0] || 'item', id: subjId }] : [],
          payload: d,
        })
        if (e) h.onEvents([e])
      },
      (s) => h.onStatus(s),
    )
  }
}

/** Fans several sources into one. Snapshots merge; each source's failure is isolated. */
export class CompositeSource implements ConsoleSource {
  readonly id: string
  private readonly sources: ConsoleSource[]
  constructor(sources: ConsoleSource[]) {
    this.sources = sources
    this.id = sources.map((s) => s.id).join('+')
  }

  async snapshot(opts?: SnapshotOptions): Promise<ConsoleSnapshot> {
    const results = await Promise.allSettled(this.sources.map((s) => s.snapshot(opts)))
    const ok = results.filter((r): r is PromiseFulfilledResult<ConsoleSnapshot> => r.status === 'fulfilled')
    if (ok.length === 0) throw new Error('All console sources failed')
    return { events: ok.flatMap((r) => r.value.events), cursor: null }
  }

  subscribe(h: SubscribeHandlers): () => void {
    const status = new Map<number, 'connecting' | 'live' | 'error'>()
    const report = () => {
      const all = [...status.values()]
      h.onStatus(all.includes('live') ? 'live' : all.every((s) => s === 'error') ? 'error' : 'connecting')
    }
    const offs = this.sources.map((s, i) =>
      s.subscribe({
        onEvents: (e) => h.onEvents(e),
        onGap: h.onGap,
        onStatus: (st) => {
          const was = status.get(i)
          status.set(i, st)
          if (st === 'live' && was !== 'live') h.onStatus('live')
          else report()
        },
      }),
    )
    return () => offs.forEach((o) => o())
  }
}
