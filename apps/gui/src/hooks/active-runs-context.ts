import { createContext, useContext } from 'react'

export type ActivityKind = 'note' | 'artifact' | 'tokens' | 'tool_use'

export interface ActivityItem {
  id: string
  kind: ActivityKind
  at: string
  text?: string
  artifact?: {
    artifact_type: string
    content?: string
    url?: string
    file_path?: string
  }
  // One usage event's delta, as the server sent it.
  tokens?: {
    prompt: number
    completion: number
    cost: number
    cache_read?: number
    cache_write?: number
  }
  tool_use?: { tool_name: string; args_summary: string }
}

export interface ActiveRun {
  taskId: string
  runId: number
  startedAt: string
  executor?: string
  lastNote?: string
  lastArtifact?: ActivityItem['artifact']
  // The run's cumulative usage so far. See accumulateTokenTotals.
  tokenTotals?: TokenTotals
  lastToolUse?: ActivityItem['tool_use']
  // ISO timestamp of the most recent heartbeat. Presence signals that the
  // run is genuinely alive — the empty state flips from "Waiting for the
  // agent's first update" to "Agent working" once a heartbeat has landed.
  lastHeartbeatAt?: string
  feed: ActivityItem[]
}

export interface ActiveRunsContextValue {
  activeRuns: ReadonlyMap<string, ActiveRun>
  connected: boolean
}

// TokenTotals is a run's consumed usage, summed across every usage event
// (one per turn for claude/codex, one per step for OpenCode). Every field is
// a billed/consumed count, so summing is right for all of them. A runtime's
// per-step context size (OpenCode's tokens.total) is never carried here:
// summing it would count the whole context once per step.
export interface TokenTotals {
  prompt: number
  completion: number
  cache_read: number
  cache_write: number
  cost: number
}

function num(v: unknown): number {
  const n = Number(v ?? 0)
  return Number.isFinite(n) ? n : 0
}

// accumulateTokenTotals folds one run.progress kind=tokens payload into the
// run's totals. The server sends its own running sum as payload.totals,
// counted before it throttles emissions, so when present it replaces ours
// outright: summing the deltas we happened to receive would miss every
// event the throttle dropped. Servers that predate totals get the best-effort
// sum of the deltas instead.
export function accumulateTokenTotals(
  prev: TokenTotals | undefined,
  payload: Record<string, unknown>,
): TokenTotals {
  const totals = payload.totals
  if (totals && typeof totals === 'object') {
    const t = totals as Record<string, unknown>
    return {
      prompt: num(t.prompt),
      completion: num(t.completion),
      cache_read: num(t.cache_read),
      cache_write: num(t.cache_write),
      cost: num(t.cost),
    }
  }
  const base = prev ?? { prompt: 0, completion: 0, cache_read: 0, cache_write: 0, cost: 0 }
  return {
    prompt: base.prompt + num(payload.prompt),
    completion: base.completion + num(payload.completion),
    cache_read: base.cache_read + num(payload.cache_read),
    cache_write: base.cache_write + num(payload.cache_write),
    cost: base.cost + num(payload.cost),
  }
}

export const ActiveRunsContext = createContext<ActiveRunsContextValue | null>(
  null,
)

// Feed cap per run — keeps long runs from ballooning memory. Activity panels
// only render the most recent handful; older items scroll off.
export const FEED_LIMIT = 100

export function useActiveRuns(): ActiveRunsContextValue {
  const ctx = useContext(ActiveRunsContext)
  if (!ctx) {
    throw new Error('useActiveRuns must be used within an ActiveRunsProvider')
  }
  return ctx
}

// useActiveRun returns the active run for a single task, or null if the task
// has no active run. Cheap wrapper so consumers don't need to read the Map
// directly.
export function useActiveRun(taskId: string | undefined): ActiveRun | null {
  const { activeRuns } = useActiveRuns()
  if (!taskId) return null
  return activeRuns.get(taskId) ?? null
}
