import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { StatusBadge } from '@/components/domain/status-badge'
import { CopyableId } from '@/components/domain/copyable-id'
import { RowActions } from '@/components/domain/row-actions'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import type { Project, ContainerStatus } from '@/lib/types'

type SortKey = 'name' | 'status' | 'repo_path' | 'updated_at'
type SortDir = 'asc' | 'desc'

const SSE_EVENTS = ['project.created', 'project.updated', 'project.deleted']

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full rounded-md" />
      ))}
    </div>
  )
}

function sortProjects(projects: Project[], key: SortKey, dir: SortDir): Project[] {
  return [...projects].sort((a, b) => {
    const av = a[key] ?? ''
    const bv = b[key] ?? ''
    const cmp = av < bv ? -1 : av > bv ? 1 : 0
    return dir === 'asc' ? cmp : -cmp
  })
}

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Project' },
  { key: 'status', label: 'Status' },
  { key: 'repo_path', label: 'Repo' },
  { key: 'updated_at', label: 'Updated' },
]

const STATUS_FILTERS: { label: string; value: ContainerStatus | 'all' }[] = [
  { label: 'All', value: 'all' },
  { label: 'Active', value: 'active' },
  { label: 'Inactive', value: 'inactive' },
]

export default function ProjectsPage() {
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState<ContainerStatus | 'all'>('all')
  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const fetchProjects = useCallback(async () => {
    try {
      const result = await api.listProjects(
        statusFilter === 'all' ? undefined : statusFilter
      )
      setProjects(result.projects)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load projects')
    } finally {
      setLoading(false)
    }
  }, [api, statusFilter])

  useEffect(() => {
    setLoading(true)
    fetchProjects()
  }, [fetchProjects])

  useEffect(() => {
    if (lastEvent) fetchProjects()
  }, [lastEvent, fetchProjects])

  function handleSortClick(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  async function handleToggleStatus(project: Project) {
    try {
      const newStatus: ContainerStatus = project.status === 'active' ? 'inactive' : 'active'
      await api.updateProject(project.id, { status: newStatus })
      fetchProjects()
    } catch {
      // no-op
    }
  }

  async function handleDelete(id: string) {
    try {
      await api.deleteProject(id)
      fetchProjects()
    } catch {
      // no-op
    }
  }

  const activeCount = projects.filter((p) => p.status === 'active').length
  const inactiveCount = projects.filter((p) => p.status === 'inactive').length

  const summaryCards = [
    { label: 'Total Projects', value: projects.length },
    { label: 'Active', value: activeCount, accentColor: '#34d399' },
    { label: 'Inactive', value: inactiveCount },
    { label: 'Catalog', value: projects.length, subtitle: 'all' },
  ]

  const sorted = sortProjects(projects, sortKey, sortDir)

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Projects">
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
            action={{ label: 'Retry', onClick: fetchProjects }}
          />
        ) : projects.length === 0 ? (
          <EmptyState
            variant="no-tasks"
            title="No projects yet"
            description="Create your first project to get started."
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
                  <th className="w-px px-2 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
                {sorted.map((project) => (
                  <tr
                    key={project.id}
                    className="cursor-pointer hover:bg-zinc-900/50 transition-colors"
                    onClick={() => navigate(`/projects/${project.id}`)}
                  >
                    <td className="px-3 py-2.5">
                      <div className="flex items-center gap-2.5">
                        <div className="flex h-7 w-7 items-center justify-center rounded border border-zinc-800 bg-zinc-900 text-[10px] font-bold uppercase text-zinc-300">
                          {project.icon || project.name.slice(0, 2)}
                        </div>
                        <div className="flex flex-col gap-0.5 min-w-0">
                          <span className="font-medium text-zinc-100 truncate">{project.name}</span>
                          <CopyableId id={project.id} />
                        </div>
                      </div>
                    </td>
                    <td className="px-2 py-2.5 text-right">
                      <StatusBadge status={project.status} />
                    </td>
                    <td className="px-2 py-2.5 text-right">
                      {project.repo_path ? (
                        <span className="font-mono text-xs text-zinc-400 max-w-[200px] truncate inline-block">
                          {project.repo_path}
                        </span>
                      ) : (
                        <span className="text-zinc-600">&mdash;</span>
                      )}
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                      {new Date(project.updated_at).toLocaleDateString()}
                    </td>
                    <td className="px-2 py-2.5 text-right" onClick={(e) => e.stopPropagation()}>
                      <RowActions
                        actions={[
                          {
                            label: project.status === 'active' ? 'Deactivate' : 'Activate',
                            onClick: () => handleToggleStatus(project),
                          },
                          {
                            label: 'Delete',
                            onClick: () => handleDelete(project.id),
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
