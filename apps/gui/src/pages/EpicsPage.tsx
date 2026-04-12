import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { StatusBadge } from '@/components/domain/status-badge'
import { CopyableId } from '@/components/domain/copyable-id'
import { PriorityBadge } from '@/components/domain/priority-badge'
import { RowActions } from '@/components/domain/row-actions'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import type { Epic, ContainerStatus } from '@/lib/types'

type SortKey = 'name' | 'status' | 'updated_at'
type SortDir = 'asc' | 'desc'

const SSE_EVENTS = ['epic.created', 'epic.updated', 'epic.deleted']

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full rounded-md" />
      ))}
    </div>
  )
}

function sortEpics(epics: Epic[], key: SortKey, dir: SortDir): Epic[] {
  return [...epics].sort((a, b) => {
    const av = a[key] ?? ''
    const bv = b[key] ?? ''
    const cmp = av < bv ? -1 : av > bv ? 1 : 0
    return dir === 'asc' ? cmp : -cmp
  })
}

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Epic' },
  { key: 'status', label: 'Status' },
]

const STATUS_FILTERS: { label: string; value: ContainerStatus | 'all' }[] = [
  { label: 'All', value: 'all' },
  { label: 'Active', value: 'active' },
  { label: 'Inactive', value: 'inactive' },
]

export default function EpicsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [epics, setEpics] = useState<Epic[]>([])
  const [projectMap, setProjectMap] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<ContainerStatus | 'all'>('all')
  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const fetchEpics = useCallback(async () => {
    try {
      const result = await api.listEpics(
        statusFilter === 'all' ? undefined : { status: statusFilter }
      )
      setEpics(result.epics)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load epics')
    } finally {
      setLoading(false)
    }
  }, [api, statusFilter])

  // Load project name lookup map
  useEffect(() => {
    api.listProjects()
      .then((r) => {
        const map: Record<string, string> = {}
        for (const p of r.projects) {
          map[p.id] = p.name
        }
        setProjectMap(map)
      })
      .catch(() => {})
  }, [api])

  useEffect(() => {
    setLoading(true)
    fetchEpics()
  }, [fetchEpics])

  useEffect(() => {
    if (lastEvent) fetchEpics()
  }, [lastEvent, fetchEpics])

  function handleSortClick(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  async function handleToggleStatus(epic: Epic) {
    try {
      const newStatus: ContainerStatus = epic.status === 'active' ? 'inactive' : 'active'
      await api.updateEpic(epic.id, { status: newStatus })
      fetchEpics()
    } catch (err) {
      notifyError(err, 'Failed to update epic status')
    }
  }

  async function handleDelete(id: string) {
    try {
      await api.deleteEpic(id)
      fetchEpics()
    } catch (err) {
      notifyError(err, 'Failed to delete epic')
    }
  }

  const activeCount = epics.filter((e) => e.status === 'active').length
  const inactiveCount = epics.filter((e) => e.status === 'inactive').length

  const summaryCards = [
    { label: 'Total Epics', value: epics.length },
    { label: 'Active', value: activeCount, accentColor: '#a78bfa' },
    { label: 'Inactive', value: inactiveCount },
    { label: 'Catalog', value: epics.length, subtitle: 'all' },
  ]

  const sorted = sortEpics(epics, sortKey, sortDir)

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Epics">
        <Button size="sm" variant="outline" className="text-xs h-7">
          New
        </Button>
      </PageHeader>

      {!loading && !error && <SummaryCards cards={summaryCards} />}

      {/* Status filter */}
      <div className="flex items-center gap-1 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5 text-xs">
        <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Status:</span>
        {STATUS_FILTERS.map(({ label, value }) => (
          <button
            key={value}
            type="button"
            onClick={() => setStatusFilter(value)}
            className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
              statusFilter === value
                ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
                : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : error ? (
          <EmptyState
            variant="error"
            description={error}
            action={{ label: 'Retry', onClick: fetchEpics }}
          />
        ) : epics.length === 0 ? (
          <EmptyState
            variant="no-tasks"
            title="No epics yet"
            description="Create your first epic to get started."
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full">
              <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
                <tr className="border-b border-zinc-800/80">
                  {COLUMNS.map(({ key, label }) => {
                    const isSorted = sortKey === key
                    const isName = key === 'name'
                    return (
                      <th
                        key={key}
                        className={`py-2 font-medium ${isName ? 'px-3 text-left' : 'w-px whitespace-nowrap px-2 text-right'}`}
                      >
                        <button
                          type="button"
                          className="inline-flex items-center gap-1 hover:text-zinc-300 transition-colors"
                          onClick={() => handleSortClick(key)}
                        >
                          {label}
                          <span className={isSorted ? 'text-zinc-200' : 'text-zinc-700'}>
                            {isSorted ? (sortDir === 'asc' ? '\u2191' : '\u2193') : '\u21D5'}
                          </span>
                        </button>
                      </th>
                    )
                  })}
                  <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">Priority</th>
                  <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">Project</th>
                  <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">
                    <button
                      type="button"
                      className="inline-flex items-center gap-1 hover:text-zinc-300 transition-colors"
                      onClick={() => handleSortClick('updated_at')}
                    >
                      Updated
                      <span className={sortKey === 'updated_at' ? 'text-zinc-200' : 'text-zinc-700'}>
                        {sortKey === 'updated_at' ? (sortDir === 'asc' ? '\u2191' : '\u2193') : '\u21D5'}
                      </span>
                    </button>
                  </th>
                  <th className="w-px px-2 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
                {sorted.map((epic) => (
                  <tr
                    key={epic.id}
                    className="cursor-pointer hover:bg-zinc-900/50 transition-colors"
                    onClick={() => navigate(`/epics/${epic.id}`)}
                  >
                    <td className="px-3 py-2.5">
                      <div className="flex flex-col gap-0.5 min-w-0">
                        <span className="font-medium text-zinc-100 truncate">{epic.name}</span>
                        <CopyableId id={epic.id} />
                      </div>
                    </td>
                    <td className="px-2 py-2.5 text-right">
                      <StatusBadge status={epic.status} />
                    </td>
                    <td className="px-2 py-2.5 text-right">
                      {epic.priority !== null && epic.priority !== undefined ? (
                        <PriorityBadge priority={epic.priority} />
                      ) : (
                        <span className="text-zinc-600">&mdash;</span>
                      )}
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                      {epic.project_id && projectMap[epic.project_id]
                        ? projectMap[epic.project_id]
                        : '\u2014'}
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                      {new Date(epic.updated_at).toLocaleDateString()}
                    </td>
                    <td className="px-2 py-2.5 text-right" onClick={(e) => e.stopPropagation()}>
                      <RowActions
                        actions={[
                          {
                            label: epic.status === 'active' ? 'Deactivate' : 'Activate',
                            onClick: () => handleToggleStatus(epic),
                          },
                          {
                            label: 'Delete',
                            onClick: () => handleDelete(epic.id),
                            variant: 'destructive',
                          },
                        ]}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
