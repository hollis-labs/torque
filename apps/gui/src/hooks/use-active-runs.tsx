import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useApi } from './use-api'
import {
  ActiveRunsContext,
  FEED_LIMIT,
  type ActiveRun,
  type ActiveRunsContextValue,
  type ActivityItem,
} from './active-runs-context'
import type { SSEEvent } from '@/lib/types'

interface ActiveRunsProviderProps {
  children: ReactNode
}

// ActiveRunsProvider subscribes to the SSE bus once and maintains a map of
// in-flight runs keyed by task_id. Consumers read from the map via
// useActiveRuns / useActiveRun in './active-runs-context'. Isolated in its
// own tsx file so fast-refresh stays happy (hook exports live next door).
export function ActiveRunsProvider({ children }: ActiveRunsProviderProps) {
  const api = useApi()
  const [activeRuns, setActiveRuns] = useState<Map<string, ActiveRun>>(
    () => new Map(),
  )
  const [connected, setConnected] = useState(false)
  const idCounter = useRef(0)

  useEffect(() => {
    function nextItemId(): string {
      idCounter.current += 1
      return `${Date.now()}-${idCounter.current}`
    }

    const unsubscribe = api.subscribeEvents((event: SSEEvent) => {
      setConnected(true)
      if (!event.type || !event.type.startsWith('run.')) return

      const data = (event.data ?? {}) as Record<string, unknown>
      const taskId = String(data.task_id ?? '')
      const runId = Number(data.run_id ?? 0)
      if (!taskId || runId <= 0) return

      const payload = (data.payload ?? {}) as Record<string, unknown>

      setActiveRuns((prev) => {
        const next = new Map(prev)

        switch (event.type) {
          case 'run.started': {
            const startedAt =
              typeof payload.started_at === 'string'
                ? payload.started_at
                : new Date().toISOString()
            next.set(taskId, {
              taskId,
              runId,
              startedAt,
              executor:
                typeof payload.executor === 'string'
                  ? payload.executor
                  : undefined,
              feed: [],
            })
            return next
          }

          case 'run.progress': {
            // Progress may arrive before run.started if the SSE stream
            // reconnected mid-run; synthesize a minimal entry so the UI
            // still tracks rather than dropping updates.
            const existing: ActiveRun =
              next.get(taskId) ?? {
                taskId,
                runId,
                startedAt: new Date().toISOString(),
                feed: [],
              }

            const kind = payload.kind
            const item: ActivityItem = {
              id: nextItemId(),
              kind: 'note',
              at: new Date().toISOString(),
            }

            const updated: ActiveRun = { ...existing, runId }

            if (kind === 'note') {
              const text =
                typeof payload.text === 'string' ? payload.text : ''
              item.kind = 'note'
              item.text = text
              updated.lastNote = text
            } else if (kind === 'artifact') {
              const artifact = {
                artifact_type:
                  typeof payload.artifact_type === 'string'
                    ? payload.artifact_type
                    : 'unknown',
                content:
                  typeof payload.content === 'string'
                    ? payload.content
                    : undefined,
                url:
                  typeof payload.url === 'string' ? payload.url : undefined,
                file_path:
                  typeof payload.file_path === 'string'
                    ? payload.file_path
                    : undefined,
              }
              item.kind = 'artifact'
              item.artifact = artifact
              updated.lastArtifact = artifact
            } else if (kind === 'tokens') {
              const tokens = {
                prompt: Number(payload.prompt ?? 0),
                completion: Number(payload.completion ?? 0),
                cost: Number(payload.cost ?? 0),
              }
              item.kind = 'tokens'
              item.tokens = tokens
              updated.lastTokens = tokens
            } else if (kind === 'tool_use') {
              const toolUse = {
                tool_name:
                  typeof payload.tool_name === 'string'
                    ? payload.tool_name
                    : 'tool',
                args_summary:
                  typeof payload.args_summary === 'string'
                    ? payload.args_summary
                    : '',
              }
              item.kind = 'tool_use'
              item.tool_use = toolUse
              updated.lastToolUse = toolUse
            } else if (kind === 'heartbeat') {
              // Heartbeats bump liveness state but never enter the feed —
              // otherwise a long run would be dominated by a wall of
              // "heartbeat" rows with no signal value.
              updated.lastHeartbeatAt = new Date().toISOString()
              next.set(taskId, updated)
              return next
            } else {
              return next
            }

            const feed = [item, ...existing.feed].slice(0, FEED_LIMIT)
            updated.feed = feed
            next.set(taskId, updated)
            return next
          }

          case 'run.finished': {
            // Drop the entry so TasksTable pulse stops and Activity panel
            // tears down. The final task status (done/review/blocked) is the
            // canonical post-run state — surfaced elsewhere in the UI.
            if (next.has(taskId)) next.delete(taskId)
            return next
          }

          default:
            return prev
        }
      })
    })

    return () => {
      unsubscribe()
    }
  }, [api])

  const value = useMemo<ActiveRunsContextValue>(
    () => ({ activeRuns, connected }),
    [activeRuns, connected],
  )

  return (
    <ActiveRunsContext.Provider value={value}>
      {children}
    </ActiveRunsContext.Provider>
  )
}
