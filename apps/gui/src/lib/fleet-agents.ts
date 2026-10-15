// Live agent activity as served by the activity-monitor plugin (relayed by
// Torque at /api/v1/activity/agents). Every string here comes from agent
// processes, so it is untrusted: callers render it as text only.

export type FleetState = 'waiting' | 'busy' | 'idle' | 'unknown'
export type Confidence = 'exact' | 'derived' | 'inferred'

export interface FleetAgent {
  id: string
  provider: string
  pid: number
  sessionId: string | null
  cwd: string
  state: FleetState
  waitingFor: string | null
  /** Epoch ms, or null when the source did not report it. */
  startedAt: number | null
  lastActivity: number | null
  /** Confidence tier of the state field. */
  stateConfidence: Confidence | null
}

const STATES: FleetState[] = ['waiting', 'busy', 'idle', 'unknown']
const TIERS: Confidence[] = ['exact', 'derived', 'inferred']
const MAX_TEXT = 200

function text(v: unknown): string {
  return typeof v === 'string' ? v.slice(0, MAX_TEXT) : ''
}

function time(v: unknown): number | null {
  if (typeof v !== 'string') return null
  const t = Date.parse(v)
  return Number.isNaN(t) ? null : t
}

/** Validates the snapshot body; rows without an id are dropped, odd values degrade to 'unknown'. */
export function parseFleetAgents(body: unknown): FleetAgent[] {
  const rows = (body as { agents?: unknown } | null)?.agents
  if (!Array.isArray(rows)) return []
  const out: FleetAgent[] = []
  for (const r of rows) {
    if (!r || typeof r !== 'object') continue
    const o = r as Record<string, unknown>
    const id = text(o.id)
    if (!id) continue
    const state = STATES.includes(o.state as FleetState) ? (o.state as FleetState) : 'unknown'
    const conf = (o.confidence as Record<string, unknown> | undefined)?.state
    out.push({
      id,
      provider: text(o.provider) || 'unknown',
      pid: typeof o.pid === 'number' ? o.pid : 0,
      sessionId: text(o.session_id) || null,
      cwd: text(o.cwd),
      state,
      waitingFor: text(o.waiting_for) || null,
      startedAt: time(o.started_at),
      lastActivity: time(o.last_activity),
      stateConfidence: TIERS.includes(conf as Confidence) ? (conf as Confidence) : null,
    })
  }
  return out
}

const ORDER: Record<FleetState, number> = { waiting: 0, busy: 1, idle: 2, unknown: 3 }

/** Waiting-on-me first, then working, idle, unknown; most recent activity first within a state. */
export function sortFleetAgents(agents: FleetAgent[]): FleetAgent[] {
  return [...agents].sort(
    (a, b) => ORDER[a.state] - ORDER[b.state] || (b.lastActivity ?? 0) - (a.lastActivity ?? 0) || a.id.localeCompare(b.id),
  )
}

export function fleetCounts(agents: FleetAgent[]): Record<FleetState, number> {
  const c: Record<FleetState, number> = { waiting: 0, busy: 0, idle: 0, unknown: 0 }
  for (const a of agents) c[a.state]++
  return c
}

/** Last two path segments: enough to recognise the project without the home prefix. */
export function cwdTail(cwd: string): string {
  const parts = cwd.split('/').filter(Boolean)
  return parts.slice(-2).join('/') || cwd || '—'
}

const KINDS_STATE = new Set(['session.idle', 'session.processing', 'session.heartbeat'])

/**
 * Applies one stream event (a go-runtime-events envelope) to the agent list.
 * process.exited removes the row; process.started and session.* upsert it from
 * the payload, which carries the full state. Anything malformed is ignored.
 * The envelope's `time` stands in for last activity: a state change is activity.
 */
export function applyFleetEvent(agents: FleetAgent[], kind: string, data: unknown): FleetAgent[] {
  if (!data || typeof data !== 'object') return agents
  const env = data as Record<string, unknown>
  const id = text(env.session_id)
  if (!id) return agents
  if (kind === 'process.exited') return agents.filter((a) => a.id !== id)
  if (kind !== 'process.started' && !KINDS_STATE.has(kind)) return agents
  const payload = (env.payload ?? {}) as Record<string, unknown>
  const proc = (env.process ?? {}) as Record<string, unknown>
  const at = time(env.time)
  const prev = agents.find((a) => a.id === id)
  const state = STATES.includes(payload.state as FleetState) ? (payload.state as FleetState) : (prev?.state ?? 'unknown')
  const next: FleetAgent = {
    id,
    provider: text(proc.provider) || prev?.provider || 'unknown',
    pid: typeof proc.pid === 'number' ? proc.pid : (prev?.pid ?? 0),
    sessionId: text(proc.provider_session_id) || prev?.sessionId || null,
    cwd: text(payload.cwd) || prev?.cwd || '',
    state,
    waitingFor: text(payload.waiting_for) || null,
    startedAt: prev?.startedAt ?? (kind === 'process.started' ? at : null),
    lastActivity: kind === 'process.started' ? (prev?.lastActivity ?? at) : (at ?? prev?.lastActivity ?? null),
    stateConfidence: prev?.stateConfidence ?? null,
  }
  return prev ? agents.map((a) => (a.id === id ? next : a)) : [...agents, next]
}
