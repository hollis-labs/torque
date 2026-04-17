import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { CopyableId } from '@/components/domain/copyable-id'
import { RowActions } from '@/components/domain/row-actions'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { Template } from '@/lib/types'

type SortKey = 'name' | 'kind' | 'updated_at'
type SortDir = 'asc' | 'desc'

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full rounded-md" />
      ))}
    </div>
  )
}

function sortTemplates(list: Template[], key: SortKey, dir: SortDir): Template[] {
  return [...list].sort((a, b) => {
    const av = a[key] ?? ''
    const bv = b[key] ?? ''
    const cmp = av < bv ? -1 : av > bv ? 1 : 0
    return dir === 'asc' ? cmp : -cmp
  })
}

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Template' },
  { key: 'kind', label: 'Kind' },
  { key: 'updated_at', label: 'Updated' },
]

export default function TemplatesPage() {
  const api = useApi()
  const navigate = useNavigate()

  const [templates, setTemplates] = useState<Template[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [includeArchived, setIncludeArchived] = useState(false)
  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const fetchTemplates = useCallback(async () => {
    try {
      const result = await api.listTemplates({ include_archived: includeArchived })
      setTemplates(result.templates)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load templates')
    } finally {
      setLoading(false)
    }
  }, [api, includeArchived])

  useEffect(() => {
    setLoading(true)
    fetchTemplates()
  }, [fetchTemplates])

  function handleSortClick(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  async function handleDelete(id: string) {
    try {
      await api.deleteTemplate(id)
      notifySuccess(`Deleted template ${id}`)
      fetchTemplates()
    } catch (err) {
      notifyError(err, 'Failed to delete template')
    }
  }

  const activeCount = templates.filter((t) => !t.is_archived).length
  const archivedCount = templates.filter((t) => t.is_archived).length
  const autoExecuteCount = templates.filter((t) => t.auto_execute).length

  const summaryCards = [
    { label: 'Total Templates', value: templates.length },
    { label: 'Active', value: activeCount, accentColor: '#34d399' },
    { label: 'Archived', value: archivedCount },
    { label: 'Auto-execute', value: autoExecuteCount, subtitle: 'on done' },
  ]

  const sorted = sortTemplates(templates, sortKey, sortDir)

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Templates" />

      {!loading && !error && <SummaryCards cards={summaryCards} />}

      {/* Archive filter */}
      <div className="flex items-center gap-1 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5 text-xs">
        <span className="mr-1 text-[10px] uppercase tracking-wider text-zinc-500">Show:</span>
        <button
          type="button"
          onClick={() => setIncludeArchived(false)}
          className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
            !includeArchived
              ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
              : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
          }`}
        >
          Active
        </button>
        <button
          type="button"
          onClick={() => setIncludeArchived(true)}
          className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
            includeArchived
              ? 'border-zinc-600 bg-zinc-800 text-zinc-200'
              : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
          }`}
        >
          All
        </button>
      </div>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : error ? (
          <EmptyState
            variant="error"
            description={error}
            action={{ label: 'Retry', onClick: fetchTemplates }}
          />
        ) : templates.length === 0 ? (
          <EmptyState
            variant="no-tasks"
            title="No templates yet"
            description="Create templates via MCP or the HTTP API to see them here."
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
                        className={`py-2 font-medium ${
                          isName ? 'px-3 text-left' : 'w-px whitespace-nowrap px-2 text-right'
                        }`}
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
                  <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">
                    Version
                  </th>
                  <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">
                    Required vars
                  </th>
                  <th className="w-px px-2 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
                {sorted.map((tpl) => (
                  <tr
                    key={`${tpl.id}@${tpl.version}`}
                    className="cursor-pointer hover:bg-zinc-900/50 transition-colors"
                    onClick={() => navigate(`/templates/${tpl.id}`)}
                  >
                    <td className="px-3 py-2.5">
                      <div className="flex flex-col gap-0.5 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-zinc-100 truncate">{tpl.name}</span>
                          {tpl.is_archived && (
                            <span className="rounded border border-zinc-700 bg-zinc-900 px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-zinc-400">
                              archived
                            </span>
                          )}
                        </div>
                        <CopyableId id={tpl.id} />
                      </div>
                    </td>
                    <td className="px-2 py-2.5 text-right">
                      <span className="inline-block rounded border border-zinc-800 bg-zinc-900 px-2 py-0.5 text-[10px] uppercase tracking-wider text-zinc-400">
                        {tpl.kind || 'agent'}
                      </span>
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                      {new Date(tpl.updated_at).toLocaleDateString()}
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap font-mono text-xs text-zinc-500">
                      v{tpl.version}
                    </td>
                    <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                      {tpl.required_vars.length > 0 ? (
                        <span className="font-mono text-xs">{tpl.required_vars.length}</span>
                      ) : (
                        <span className="text-zinc-600">&mdash;</span>
                      )}
                    </td>
                    <td className="px-2 py-2.5 text-right" onClick={(e) => e.stopPropagation()}>
                      <RowActions
                        actions={[
                          {
                            label: 'Instantiate',
                            onClick: () => navigate(`/templates/${tpl.id}?action=instantiate`),
                          },
                          {
                            label: 'Delete',
                            onClick: () => handleDelete(tpl.id),
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
