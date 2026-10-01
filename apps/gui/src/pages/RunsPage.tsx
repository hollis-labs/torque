import { useState, useEffect, useCallback, useRef } from 'react'
import { Skeleton, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, EmptyState } from '@hollis-labs/sysop-ui'
import { RunCard } from '@/components/domain/run-card'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Run } from '@/lib/types'

type StatusFilter = 'all' | 'running' | 'completed' | 'failed'

export default function RunsPage() {
  const api = useApi()
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')

  const [cursor, setCursor] = useState<string | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [sortBy, setSortBy] = useState<'started_at' | 'duration' | 'cost'>('started_at')
  const generation = useRef({ value: 0 })
  const sentinel = useRef<HTMLDivElement>(null)
  const scrollRoot = useRef<HTMLDivElement>(null)
  const runsRef = useRef(runs)
  useEffect(() => { runsRef.current = runs }, [runs])
  const { lastEvent, connected } = useSSE(['run.started', 'run.completed'])
  const serverStatus = statusFilter === 'completed' ? 'done' : statusFilter === 'all' ? undefined : statusFilter

  const load = useCallback(async () => {
    const current = ++generation.current.value
    setLoading(true)
    setLoadingMore(false)
    setCursor(null)
    try {
      const page = await api.pageRuns({ status: serverStatus, sort_by: sortBy })
      if (current !== generation.current.value) return
      setRuns(page.items)
      setCursor(page.meta.next_cursor)
      setError(null)
    } catch (err) {
      if (current === generation.current.value) setError(err instanceof Error ? err.message : 'Failed to load runs')
    } finally {
      if (current === generation.current.value) setLoading(false)
    }
  }, [api, serverStatus, sortBy])

  useEffect(() => {
    const activeGeneration = generation.current
    void load()
    return () => { activeGeneration.value++ }
  }, [load])

  const loadMore = useCallback(async () => {
    if (!cursor || loadingMore || loading) return
    const current = generation.current.value
    setLoadingMore(true)
    try {
      const page = await api.pageRuns({ status: serverStatus, sort_by: sortBy, cursor })
      if (current !== generation.current.value) return
      setRuns((prev) => [...prev, ...page.items.filter((row) => !prev.some((shown) => shown.id === row.id))])
      setCursor(page.meta.next_cursor)
      setError(null)
    } catch (err) {
      if (current === generation.current.value) setError(err instanceof Error ? err.message : 'Failed to load older runs')
    } finally {
      if (current === generation.current.value) setLoadingMore(false)
    }
  }, [api, cursor, loadingMore, loading, serverStatus, sortBy])

  useEffect(() => {
    if (!cursor || loading || loadingMore || error || !sentinel.current) return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMore()
    }, { root: scrollRoot.current, rootMargin: '200px' })
    observer.observe(sentinel.current)
    return () => observer.disconnect()
  }, [cursor, loading, loadingMore, error, loadMore])

  // Patch loaded rows only; new runs appear on Refresh without shifting a
  // cursor already being browsed. The server controls membership and sort.
  useEffect(() => {
    const runId = lastEvent?.data.run_id
    if (typeof runId !== 'number' || !runsRef.current.some((row) => row.id === runId)) return
    const current = generation.current.value
    void api.getRun(runId).then((updated) => {
      if (current !== generation.current.value) return
      setRuns((prev) => prev.flatMap((row) => row.id !== runId ? [row] :
        serverStatus && updated.status !== serverStatus ? [] : [updated]))
    }).catch(() => {})
  }, [lastEvent, api, serverStatus, statusFilter])

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
        <div className="flex items-center gap-2">
        <button className="text-sm text-primary" onClick={() => void load()}>Refresh</button>
        <Select value={sortBy} onValueChange={(v) => setSortBy(v as typeof sortBy)}>
          <SelectTrigger className="h-8 w-36 text-sm"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="started_at">Newest first</SelectItem>
            <SelectItem value="duration">Longest first</SelectItem>
            <SelectItem value="cost">Highest cost</SelectItem>
          </SelectContent>
        </Select>
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
      </div>

      {/* Content */}
      <div ref={scrollRoot} className="flex-1 overflow-auto p-6">
        {loading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-28 w-full rounded-lg" />
            ))}
          </div>
        ) : error ? (
          <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: () => load() }} />
        ) : runs.length === 0 ? (
          <EmptyState
            variant="no-results"
            title={statusFilter === 'all' ? 'No runs yet' : `No ${statusFilter} runs`}
            description="Runs appear here once tasks have been executed."
          />
        ) : (
          <div className="flex flex-col gap-3 max-w-2xl">
            {runs.map((run) => (
              <RunCard key={run.id} run={run} showTaskLink />
            ))}
          </div>
        )}
        {cursor && !error && <div ref={sentinel} className="py-4">
          <button className="text-sm text-primary" disabled={loadingMore} onClick={() => void loadMore()}>
            {loadingMore ? 'Loading…' : 'Load older runs'}
          </button>
        </div>}
      </div>
    </div>
  )
}
