import { ago } from '@/lib/agent-activity'
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
  /** Exact model id (e.g. "claude-opus-4-7"), when the source reports it. */
  model: string | null
  /** What the agent last did, e.g. "Searched tesseract"; null until the source reports it. */
  lastAction: { text: string; at: number | null } | null
  /** Workstream the agent is working under, when the source knows it. */
  workstream: { id: string; name: string | null } | null
  /** Unread messages waiting for the agent; null when the source does not report mail. */
  messages: number | null
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

/** The fields the source may or may not carry yet; absent ones stay null and the page degrades. */
function optionalFields(o: Record<string, unknown>): Pick<FleetAgent, 'model' | 'lastAction' | 'workstream' | 'messages'> {
  const action = text(o.last_action)
  const wsId = text(o.workstream_id)
  const unread = o.unread_messages ?? o.messages
  return {
    model: text(o.model) || null,
    lastAction: action ? { text: action, at: time(o.last_action_at) } : null,
    workstream: wsId ? { id: wsId, name: text(o.workstream_name) || null } : null,
    messages: typeof unread === 'number' && unread >= 0 ? Math.floor(unread) : null,
  }
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
      ...optionalFields(o),
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
    model: prev?.model ?? null,
    lastAction: prev?.lastAction ?? null,
    workstream: prev?.workstream ?? null,
    messages: prev?.messages ?? null,
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

/** Per-bucket busy share (0-100) across the window, oldest first; unobserved time counts as not busy. */
export function busySeries(entries: FleetTransition[], now: number, windowMs: number, buckets = 24): number[] {
  const size = windowMs / buckets
  const start = now - windowMs
  const busy = Array<number>(buckets).fill(0)
  for (const g of stateSegments(entries, now, windowMs)) {
    if (g.state !== 'busy') continue
    for (let i = 0; i < buckets; i++) {
      const lo = start + i * size
      const overlap = Math.min(g.to, lo + size) - Math.max(g.from, lo)
      if (overlap > 0) busy[i] += overlap
    }
  }
  return busy.map((ms) => Math.round((ms / size) * 100))
}

/**
 * TEMPORARY leaderboard score, built only from what the live feed carries:
 * observed working share, how long the agent has been up (capped at 12h), and
 * how recently it did anything. An agent waiting on a person or with an unknown
 * state is docked. To be replaced once longer history and token/cost data exist.
 */
export function fleetScore(agent: FleetAgent, entries: FleetTransition[], now: number): number {
  const share = workingShare(entries, now, timelineWindow(entries, now)) ?? 0
  const upHours = agent.startedAt ? Math.min(12, (now - agent.startedAt) / 3_600_000) : 0
  const idleMin = agent.lastActivity ? Math.max(0, (now - agent.lastActivity) / 60_000) : 60
  const recency = Math.max(0, 20 - idleMin / 3)
  const dock = agent.state === 'unknown' ? 20 : agent.state === 'waiting' ? 5 : 0
  return Math.max(0, Math.round(share * 5 + upHours * 10 + recency - dock))
}

export interface FleetGroup {
  /** Workstream id when the group is a real workstream; null for the working-directory fallback. */
  workstreamId: string | null
  name: string
  agents: Array<{ agent: FleetAgent; score: number; rank: number }>
  score: number
}

/**
 * Groups by workstream; agents the source has not placed in one fall back to
 * their working-directory name. Rows are ranked by score inside a group and
 * groups by their total, so the leaderboard reads top to bottom.
 */
export function groupFleet(agents: FleetAgent[], history: FleetHistory, now: number): FleetGroup[] {
  const buckets = new Map<string, FleetGroup>()
  for (const agent of agents) {
    const ws = agent.workstream
    const name = ws ? ws.name || ws.id : cwdName(agent.cwd) || agent.provider
    const key = ws ? `ws:${ws.id}` : `dir:${name}`
    const group = buckets.get(key) ?? { workstreamId: ws?.id ?? null, name, agents: [], score: 0 }
    group.agents.push({ agent, score: fleetScore(agent, history[agent.id] ?? [], now), rank: 0 })
    buckets.set(key, group)
  }
  const groups = [...buckets.values()]
  for (const g of groups) {
    g.agents.sort((a, b) => b.score - a.score || a.agent.id.localeCompare(b.agent.id))
    g.agents.forEach((a, i) => (a.rank = i + 1))
    g.score = g.agents.reduce((sum, a) => sum + a.score, 0)
  }
  return groups.sort((a, b) => b.score - a.score || a.name.localeCompare(b.name))
}

/** "Searched tesseract · just now" when the source reports an action, else when it was last active. */
export function whatNow(agent: FleetAgent, now: number): string {
  if (agent.lastAction) return `${agent.lastAction.text} · ${ago(agent.lastAction.at ?? agent.lastActivity ?? now, now)}`
  return agent.lastActivity ? `Active ${ago(agent.lastActivity, now)}` : 'No activity data'
}

/** The agent a `?agent=` value names: exact id first, then provider, then working-folder name; first match wins. */
export function matchFocusedAgent(agents: FleetAgent[], focus: string): FleetAgent | null {
  const f = focus.trim().toLowerCase()
  if (!f) return null
  const by = (pick: (a: FleetAgent) => string) => agents.find((a) => pick(a).toLowerCase() === f) ?? null
  return by((a) => a.id) ?? by((a) => a.provider) ?? by((a) => cwdName(a.cwd))
}
