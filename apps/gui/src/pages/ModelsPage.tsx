import { useEffect, useMemo, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { useApi } from '@/hooks/use-api'
import { notifyError } from '@/lib/toast'
import { PageHeader } from '@/components/domain/page-header'
import { FilterEntityCombobox } from '@/components/domain/filter-bar/filter-entity-combobox'
import type { ModelEntry } from '@/lib/types'

type SortKey = 'provider' | 'name' | 'context' | 'output' | 'cost'

interface SortState {
  key: SortKey
  dir: 'asc' | 'desc'
}

export default function ModelsPage() {
  const api = useApi()
  const [models, setModels] = useState<ModelEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [providerFilter, setProviderFilter] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<SortState>({ key: 'provider', dir: 'asc' })

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const result = await api.listModels()
        if (!cancelled) {
          setModels(result)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to load models')
          notifyError(err, 'Failed to load models')
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [api])

  const providers = useMemo(() => {
    const set = new Set(models.map((m) => m.provider_id))
    return Array.from(set)
      .sort()
      .map((id) => ({ id, name: id }))
  }, [models])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return models.filter((m) => {
      if (providerFilter && m.provider_id !== providerFilter) return false
      if (q && !m.id.toLowerCase().includes(q) && !m.name.toLowerCase().includes(q)) return false
      return true
    })
  }, [models, providerFilter, search])

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      let av: string | number = ''
      let bv: string | number = ''
      switch (sort.key) {
        case 'provider':
          av = a.provider_id
          bv = b.provider_id
          break
        case 'name':
          av = a.name || a.id
          bv = b.name || b.id
          break
        case 'context':
          av = a.limit?.context_window ?? 0
          bv = b.limit?.context_window ?? 0
          break
        case 'output':
          av = a.limit?.max_output_tokens ?? 0
          bv = b.limit?.max_output_tokens ?? 0
          break
        case 'cost':
          av = a.cost?.input ?? 0
          bv = b.cost?.input ?? 0
          break
      }
      if (av < bv) return sort.dir === 'asc' ? -1 : 1
      if (av > bv) return sort.dir === 'asc' ? 1 : -1
      // Stable secondary sort by provider then id.
      if (a.provider_id !== b.provider_id) return a.provider_id < b.provider_id ? -1 : 1
      return a.id < b.id ? -1 : 1
    })
    return copy
  }, [filtered, sort])

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
        <FilterEntityCombobox
          icon={<Sparkles className="h-3.5 w-3.5" />}
          items={providers}
          value={providerFilter}
          onChange={setProviderFilter}
          allLabel="All providers"
          ariaLabel="Filter by provider"
        />
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search id or name…"
          className="h-7 rounded border border-zinc-800 bg-zinc-900/50 px-2 text-[12px] text-zinc-100 placeholder:text-zinc-500 focus:border-zinc-600 focus:outline-none"
        />
        <span className="ml-auto text-[10px] uppercase tracking-wider text-zinc-500">
          {sorted.length} of {models.length}
          {models.length === 0 && !loading && ' — catalog empty (cold cache?)'}
        </span>
      </div>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <div className="p-8 text-center text-[12px] text-zinc-500">Loading catalog…</div>
        ) : error ? (
          <div className="p-8 text-center text-[12px] text-rose-400">{error}</div>
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
