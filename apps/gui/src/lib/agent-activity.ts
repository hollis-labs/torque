// Agent activity monitor: the view-model, the leaderboard and the grouping.
//
// The data model here is a PLACEHOLDER shaped by the page's needs. When the real
// API is final, map its response into `AgentActivity` in `useAgentActivity`
// (src/hooks/use-agent-activity.ts); nothing else on the page should change.

export type AgentState = 'working' | 'idle' | 'blocked' | 'offline'

export interface AgentHistoryEntry {
  /** Epoch ms. */
  at: number
  kind: 'done' | 'failed' | 'started' | 'note'
  text: string
}

export interface AgentStats {
  done24h: number
  failed24h: number
  actions24h: number
  /** Consecutive successful tasks. */
  streak: number
  avgTaskMin: number | null
}

export interface AgentActivity {
  id: string
  /** The agent's readable name. */
  name: string
  role?: string
  /** Workstream or project it belongs to, when known. */
  group: string | null
  state: AgentState
  /** Epoch ms of the last action. */
  lastActionAt: number
  lastAction: string
  stats: AgentStats
  /** Actions per bucket, oldest first; drives the mini graph. */
  series: number[]
  history: AgentHistoryEntry[]
}

export const UNGROUPED = 'Unassigned'

const MIN = 60_000

/**
 * The leaderboard score. Finished work counts most, failures cost, a streak and
 * recent activity add momentum, and a long-silent agent decays toward the bottom.
 */
export function leaderboardScore(a: AgentActivity, now: number): number {
  const { done24h, failed24h, actions24h, streak } = a.stats
  const idleMin = Math.max(0, (now - a.lastActionAt) / MIN)
  const momentum = a.state === 'working' ? 15 : a.state === 'blocked' ? -5 : a.state === 'offline' ? -20 : 0
  const recency = Math.max(0, 20 - idleMin / 3) // 20 pts if just now, 0 after an hour
  return Math.round(done24h * 10 + actions24h * 0.5 + Math.min(streak, 10) * 2 - failed24h * 5 + momentum + recency)
}

export interface AgentGroup {
  name: string
  agents: Array<AgentActivity & { score: number; rank: number }>
  score: number
}

/** Groups by workstream/project; each group and each row inside it is ordered by score. */
export function groupAgents(agents: AgentActivity[], now: number): AgentGroup[] {
  const buckets = new Map<string, AgentGroup['agents']>()
  for (const a of agents) {
    const key = a.group?.trim() || UNGROUPED
    const list = buckets.get(key) ?? []
    list.push({ ...a, score: leaderboardScore(a, now), rank: 0 })
    buckets.set(key, list)
  }
  const groups: AgentGroup[] = [...buckets.entries()].map(([name, list]) => {
    list.sort((x, y) => y.score - x.score || x.name.localeCompare(y.name))
    list.forEach((a, i) => (a.rank = i + 1))
    return { name, agents: list, score: list.reduce((s, a) => s + a.score, 0) }
  })
  groups.sort((x, y) => {
    if ((x.name === UNGROUPED) !== (y.name === UNGROUPED)) return x.name === UNGROUPED ? 1 : -1
    return y.score - x.score || x.name.localeCompare(y.name)
  })
  return groups
}

/** "just now", "4m ago", "3h ago", "2d ago". */
export function ago(at: number, now: number): string {
  const s = Math.max(0, Math.round((now - at) / 1000))
  if (s < 10) return 'just now'
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 48) return `${h}h ago`
  return `${Math.round(h / 24)}d ago`
}

/** Share of finished tasks that succeeded, or null when none finished. */
export function successRate(s: AgentStats): number | null {
  const total = s.done24h + s.failed24h
  return total === 0 ? null : Math.round((s.done24h / total) * 100)
}

/** FNV-1a: a stable seed so an agent keeps its face. */
export function hashString(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

export interface RobotSprite {
  /** 8 rows x 8 cols; true = lit pixel. Mirrored left/right. */
  pixels: boolean[][]
  /** Eye cells (row, col), drawn in the eye colour and blinked. */
  eyes: Array<[number, number]>
  hue: number
}

/** A symmetric 8x8 robot derived from the agent id. */
export function robotSprite(id: string): RobotSprite {
  let seed = hashString(id)
  const next = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 0x100000000
  }
  const half: boolean[][] = Array.from({ length: 8 }, () => Array(4).fill(false))
  half[0][3] = true // antenna
  if (next() > 0.5) half[0][0] = true // ears
  for (let r = 1; r <= 4; r++) for (let c = 1; c < 4; c++) half[r][c] = true // head
  if (next() > 0.5) half[2][0] = half[3][0] = true // side bolts
  for (let r = 5; r < 8; r++) {
    half[r][3] = true
    half[r][2] = true
    half[r][1] = next() > 0.35
    half[r][0] = next() > 0.55
  }
  const pixels = half.map((row) => [...row, ...[...row].reverse()])
  const eyeRow = 2
  const eyes: Array<[number, number]> = [
    [eyeRow, 2],
    [eyeRow, 5],
  ]
  return { pixels, eyes, hue: Math.floor(next() * 360) }
}
