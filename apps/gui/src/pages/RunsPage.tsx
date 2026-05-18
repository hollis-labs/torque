import { useState, useEffect, useCallback } from 'react'
import { Skeleton, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, EmptyState } from '@hollis-labs/sysop-ui'
import { RunCard } from '@/components/domain/run-card'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Run } from '@/lib/types'

type StatusFilter = 'all' | 'running' | 'completed' | 'failed'

// Browse window for the cross-task feed. The backend clamps to [1, 1000];
// 200 keeps the page snappy while still spanning plenty of history.
const RUNS_LIMIT = 200

// Backstop poll interval — refreshes the feed even if no SSE event lands,
// covering runs missed during an SSE disconnect.
const POLL_INTERVAL_MS = 30_000

export default function RunsPage() {
  const api = useApi()
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')

  // run.started / run.finished change the run list; run.progress is omitted
  // so high-frequency mid-run updates don't trigger a refetch storm.
  const { lastEvent, connected } = useSSE(['run.started', 'run.finished'])

  const load = useCallback(
    async (silent = false) => {
      try {
        if (!silent) setLoading(true)
        const all = await api.listAllRuns({ limit: RUNS_LIMIT })
        setRuns(all)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load runs')
      } finally {
        if (!silent) setLoading(false)
      }
    },
    [api],
  )

  // Initial load (shows skeletons).
  useEffect(() => {
    load()
  }, [load])

  // Live refresh on run lifecycle events — silent so the list doesn't flash
  // skeletons while a run starts or finishes.
  useEffect(() => {
    if (lastEvent) load(true)
  }, [lastEvent, load])

  // Periodic poll as a backstop — catches runs missed while SSE was
  // disconnected. Silent, so it never disrupts the visible list.
  useEffect(() => {
    const id = setInterval(() => load(true), POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [load])

  const filtered = statusFilter === 'all'
    ? runs
    : runs.filter((r) => r.status === statusFilter)

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="border-b border-border bg-card px-6 py-4 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-lg font-semibold text-foreground">Runs</h1>
          <span
            className="flex items-center gap-1.5 text-xs text-muted-foreground"
            title={connected ? 'Live updates connected' : 'Live updates disconnected'}
          >
            <span
              className={`h-2 w-2 rounded-full ${
                connected
                  ? 'bg-[var(--color-status-done)]'
                  : 'bg-muted-foreground/40'
              }`}
            />
            {connected ? 'Live' : 'Offline'}
          </span>
        </div>
        <Select value={statusFilter} onValueChange={(v) => setStatusFilter(v as StatusFilter)}>
          <SelectTrigger className="h-8 w-36 text-sm">
            <SelectValue placeholder="All statuses" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All statuses</SelectItem>
            <SelectItem value="running">Running</SelectItem>
            <SelectItem value="completed">Completed</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto p-6">
        {loading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-28 w-full rounded-lg" />
            ))}
          </div>
        ) : error ? (
          <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: () => load() }} />
        ) : filtered.length === 0 ? (
          <EmptyState
            variant="no-results"
            title={statusFilter === 'all' ? 'No runs yet' : `No ${statusFilter} runs`}
            description="Runs appear here once tasks have been executed."
          />
        ) : (
          <div className="flex flex-col gap-3 max-w-2xl">
            {filtered.map((run) => (
              <RunCard key={run.id} run={run} showTaskLink />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
