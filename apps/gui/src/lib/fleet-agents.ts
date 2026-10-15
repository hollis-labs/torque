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
  /** Tier per reported field (alive, cwd, state, ...). */
  confidence: Record<string, Confidence>
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

function confidenceMap(v: unknown): Record<string, Confidence> {
  const out: Record<string, Confidence> = {}
  if (!v || typeof v !== 'object') return out
  for (const [k, t] of Object.entries(v as Record<string, unknown>)) {
    if (TIERS.includes(t as Confidence)) out[k.slice(0, 40)] = t as Confidence
  }
  return out
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
    const confMap = confidenceMap(o.confidence)
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
      stateConfidence: confMap.state ?? null,
      confidence: confMap,
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
    confidence: prev?.confidence ?? {},
  }
  return prev ? agents.map((a) => (a.id === id ? next : a)) : [...agents, next]
}

/** The last path segment names the working directory; the agent has no name of its own. */
export function cwdName(cwd: string): string {
  const parts = cwd.split('/').filter(Boolean)
  return parts[parts.length - 1] ?? ''
}

/** "3h 12m", "4m 05s", "12s". */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h ${String(m % 60).padStart(2, '0')}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

// What the page has seen change since it opened. The source has no history, so
// this is the only record there is: it starts when the page does.

export interface FleetTransition {
  /** Epoch ms the page observed it. */
  at: number
  /** seen: first sight of an agent already running; started: appeared later; state: state or wait changed. */
  kind: 'seen' | 'started' | 'state'
  state: FleetState
  waitingFor: string | null
}

export type FleetHistory = Record<string, FleetTransition[]>

const MAX_TRANSITIONS = 60

/** Records state changes between two agent lists. `prev` null means the first load. Rows that left are dropped. */
export function trackTransitions(history: FleetHistory, prev: FleetAgent[] | null, next: FleetAgent[], at: number): FleetHistory {
  const before = new Map((prev ?? []).map((a) => [a.id, a]))
  const out: FleetHistory = {}
  for (const a of next) {
    const list = history[a.id] ?? []
    const old = before.get(a.id)
    let entry: FleetTransition | null = null
    if (!old && list.length === 0) entry = { at, kind: prev === null ? 'seen' : 'started', state: a.state, waitingFor: a.waitingFor }
    else if (old && (old.state !== a.state || old.waitingFor !== a.waitingFor)) entry = { at, kind: 'state', state: a.state, waitingFor: a.waitingFor }
    out[a.id] = entry ? [...list, entry].slice(-MAX_TRANSITIONS) : list
  }
  return out
}

export interface StateSegment {
  /** null: before the page started watching. */
  state: FleetState | null
  from: number
  to: number
}

/** The observed states across [now - windowMs, now], with an unobserved lead-in when the page is newer than the window. */
export function stateSegments(entries: FleetTransition[], now: number, windowMs: number): StateSegment[] {
  const start = now - windowMs
  if (entries.length === 0) return [{ state: null, from: start, to: now }]
  const out: StateSegment[] = []
  if (entries[0].at > start) out.push({ state: null, from: start, to: Math.min(entries[0].at, now) })
  entries.forEach((e, i) => {
    const from = Math.max(e.at, start)
    const to = Math.min(entries[i + 1]?.at ?? now, now)
    if (to > from) out.push({ state: e.state, from, to })
  })
  return out
}

/** Milliseconds observed per state; the unobserved lead-in is left out. */
export function stateDurations(segments: StateSegment[]): Record<FleetState, number> {
  const d: Record<FleetState, number> = { waiting: 0, busy: 0, idle: 0, unknown: 0 }
  for (const g of segments) if (g.state) d[g.state] += g.to - g.from
  return d
}

/** Share of the observed time spent working, or null when nothing was observed. */
export function workingShare(entries: FleetTransition[], now: number, windowMs: number): number | null {
  const d = stateDurations(stateSegments(entries, now, windowMs))
  const total = d.waiting + d.busy + d.idle + d.unknown
  return total > 0 ? Math.round((d.busy / total) * 100) : null
}

const MIN_WINDOW_MS = 60_000
export const MAX_WINDOW_MS = 30 * 60_000

/** The timeline spans what has been watched (at least a minute, at most 30), so a fresh page is not a mostly empty strip. */
export function timelineWindow(entries: FleetTransition[], now: number): number {
  const first = entries[0]?.at ?? now
  return Math.min(MAX_WINDOW_MS, Math.max(MIN_WINDOW_MS, now - first))
}
