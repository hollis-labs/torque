import { useState } from 'react'
import { useApi } from '@/hooks/use-api'
import { PageHeader } from '@hollis-labs/sysop-ui'
import { usePagedList } from '@/hooks/use-paged-list'
import { useListSearch } from '@/hooks/use-list-search'
import { ListPageControls } from '@/components/domain/list-page-controls'

type SortKey = 'provider' | 'name' | 'context' | 'output' | 'cost'

interface SortState {
  key: SortKey
  dir: 'asc' | 'desc'
}

export default function ModelsPage() {
  const api = useApi()
  const [providerFilter, setProviderFilter] = useState('')
  const [search, setSearch] = useState('')
  const serverSearch = useListSearch(search)
  const provider = useListSearch(providerFilter)
  const [sort, setSort] = useState<SortState>({ key: 'provider', dir: 'asc' })
  const page = usePagedList({
    params: { provider, search: serverSearch, sort_by: sort.key === 'provider' ? 'provider_id' : sort.key, sort_dir: sort.dir, include_total: true },
    fetchPage: ({ params, cursor, signal }) => api.listModels(params.provider, { search: params.search, sort_by: params.sort_by, sort_dir: params.sort_dir, include_total: params.include_total, cursor }, signal),
    getId: model => `${model.provider_id}/${model.id}`,
  })
  const { items: models, loading, error } = page
  const sorted = models

  function toggleSort(key: SortKey) {
    setSort((prev) => {
      if (prev.key !== key) return { key, dir: 'asc' }
      return { key, dir: prev.dir === 'asc' ? 'desc' : 'asc' }
    })
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Models" />

      <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5">
        <input aria-label="Filter by provider" value={providerFilter} onChange={event => setProviderFilter(event.target.value)} placeholder="Provider id (all if empty)" className="h-7 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-xs" />
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search id or name…"
          className="h-7 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[12px] text-zinc-100 placeholder:text-zinc-500 focus:border-zinc-600 focus:outline-none"
        />
        <span className="ml-auto text-[10px] uppercase tracking-wider text-zinc-500">
          {models.length} of {page.total ?? '…'} loaded
          {models.length === 0 && !loading && ' — catalog empty (cold cache?)'}
        </span>
      </div>

      <ListPageControls page={page} label="models" />
      <div className="flex-1 overflow-auto">
        {loading && models.length === 0 ? (
          <div className="p-8 text-center text-[12px] text-zinc-500">Loading catalog…</div>
        ) : error && models.length === 0 ? (
          <div className="p-8 text-center text-[12px] text-rose-400">{error.message}</div>
        ) : sorted.length === 0 ? (
          <div className="p-8 text-center text-[12px] text-zinc-500">
            {models.length === 0
              ? 'No models in catalog yet — the background refresher may not have fetched. Wait ~30s and retry.'
              : 'No models match the current filter.'}
          </div>
        ) : (
          <table className="w-full text-[12px]">
            <thead className="sticky top-0 bg-zinc-950 text-[10px] uppercase tracking-wider text-zinc-500">
              <tr>
                <SortHeader col="provider" label="Provider" sort={sort} onClick={toggleSort} />
                <SortHeader col="name" label="Model" sort={sort} onClick={toggleSort} />
                <SortHeader col="context" label="Context" sort={sort} onClick={toggleSort} align="right" />
                <SortHeader col="output" label="Max Out" sort={sort} onClick={toggleSort} align="right" />
                <SortHeader col="cost" label="$/M In" sort={sort} onClick={toggleSort} align="right" />
                <th className="px-3 py-2 text-right font-normal">$/M Out</th>
                <th className="px-3 py-2 text-left font-normal">Capabilities</th>
                <th className="px-3 py-2 text-left font-normal">Updated</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((m) => (
                <tr
                  key={`${m.provider_id}/${m.id}`}
                  className="border-t border-zinc-800/60 hover:bg-zinc-900/40"
                >
                  <td className="px-3 py-2 font-mono text-zinc-400">{m.provider_id}</td>
                  <td className="px-3 py-2">
                    <div className="text-zinc-100">{m.name || m.id}</div>
                    {m.name && m.id !== m.name && (
                      <div className="font-mono text-[10px] text-zinc-500">{m.id}</div>
                    )}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-zinc-300">
                    {formatTokens(m.limit?.context_window)}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-zinc-300">
                    {formatTokens(m.limit?.max_output_tokens)}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-zinc-300">
                    {formatPrice(m.cost?.input)}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-zinc-300">
                    {formatPrice(m.cost?.output)}
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex flex-wrap gap-1">
                      {m.capabilities?.tool_call && <CapBadge label="tools" />}
                      {m.capabilities?.reasoning && <CapBadge label="reasoning" />}
                      {m.capabilities?.attachment && <CapBadge label="attach" />}
                      {m.capabilities?.temperature && <CapBadge label="temp" />}
                    </div>
                  </td>
                  <td className="px-3 py-2 font-mono text-[10px] text-zinc-500">
                    {m.last_updated || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

function SortHeader({
  col,
  label,
  sort,
  onClick,
  align = 'left',
}: {
  col: SortKey
  label: string
  sort: SortState
  onClick: (k: SortKey) => void
  align?: 'left' | 'right'
}) {
  const active = sort.key === col
  const arrow = active ? (sort.dir === 'asc' ? ' ▲' : ' ▼') : ''
  return (
    <th
      onClick={() => onClick(col)}
      className={`cursor-pointer select-none px-3 py-2 font-normal hover:text-zinc-200 ${
        align === 'right' ? 'text-right' : 'text-left'
      } ${active ? 'text-zinc-200' : ''}`}
    >
      {label}
      <span className="text-[8px]">{arrow}</span>
    </th>
  )
}

function CapBadge({ label }: { label: string }) {
  return (
    <span className="rounded border border-zinc-800 bg-zinc-900/60 px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-zinc-400">
      {label}
    </span>
  )
}

function formatTokens(n: number | undefined): string {
  if (!n || n === 0) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`
  return String(n)
}

function formatPrice(n: number | undefined): string {
  if (n === undefined || n === null || n === 0) return '—'
  return `$${n.toFixed(2)}`
}
