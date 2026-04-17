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
  tokens?: { prompt: number; completion: number; cost: number }
  tool_use?: { tool_name: string; args_summary: string }
}

export interface ActiveRun {
  taskId: string
  runId: number
  startedAt: string
  executor?: string
  lastNote?: string
  lastArtifact?: ActivityItem['artifact']
  lastTokens?: ActivityItem['tokens']
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
