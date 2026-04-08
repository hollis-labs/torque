import { useState, useEffect } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { RunCard } from '@/components/domain/run-card'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import type { Run, Task } from '@/lib/types'

type StatusFilter = 'all' | 'running' | 'completed' | 'failed'

export default function RunsPage() {
  const api = useApi()
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')

  useEffect(() => {
    async function load() {
      try {
        setLoading(true)
        // Fetch recent tasks, then their runs
        const { tasks } = await api.listTasks({ limit: 20 })
        const runArrays = await Promise.all(
          tasks.map((t: Task) => api.listRuns(t.id).catch(() => [] as Run[]))
        )
        const all = runArrays.flat().sort((a, b) => {
          const ta = new Date(a.started_at).getTime()
          const tb = new Date(b.started_at).getTime()
          return tb - ta
        })
        setRuns(all)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load runs')
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [api])

  const filtered = statusFilter === 'all'
    ? runs
    : runs.filter((r) => r.status === statusFilter)

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="border-b border-border bg-card px-6 py-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-foreground">Runs</h1>
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
          <EmptyState variant="error" description={error} action={{ label: 'Retry', onClick: () => window.location.reload() }} />
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
