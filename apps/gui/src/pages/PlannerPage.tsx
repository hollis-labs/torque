import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Plus, X } from 'lucide-react'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { Button, EmptyState, Input, PageHeader, Skeleton } from '@hollis-labs/sysop-ui'
import { PlannerCreateDialog } from '@/components/planner/planner-create-dialog'
import { PlannerDetailDialog } from '@/components/planner/planner-detail-dialog'
import { PlannerRow } from '@/components/planner/planner-row'
import { PlannerStatus } from '@/components/planner/planner-status'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { ApiError } from '@/lib/api'
import { PLANNER_DBS, filterItems, isPlannerDb, isSettled, moveId, plannerMeta, statusCounts } from '@/lib/planner'
import { notifyError } from '@/lib/toast'
import type { PlannerDatabaseInfo, PlannerDb, PlannerItem, PlannerSearchHit } from '@/lib/types'

const SSE_EVENTS = ['planner.changed']

export default function PlannerPage() {
  const api = useApi()
  const navigate = useNavigate()
  const params = useParams<{ db?: string; id?: string }>()
  const db: PlannerDb = isPlannerDb(params.db) ? params.db : 'priorities'
  const openId = params.id ? decodeURIComponent(params.id) : undefined
  const meta = plannerMeta(db)

  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [counts, setCounts] = useState<PlannerDatabaseInfo[]>([])
  const [items, setItems] = useState<PlannerItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState('')
  const [query, setQuery] = useState('')
  const [showSettled, setShowSettled] = useState(false)
  const [search, setSearch] = useState('')
  const [hits, setHits] = useState<PlannerSearchHit[] | null>(null)
  const [creating, setCreating] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const { lastEvent } = useSSE(SSE_EVENTS)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))

  const loadCounts = useCallback(async () => {
    try {
      setCounts(await api.plannerDatabases())
      setEnabled(true)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setError(err instanceof Error ? err.message : 'Failed to load the planner')
    }
  }, [api])

  const loadItems = useCallback(async () => {
    try {
      setItems(await api.plannerList(db))
      setError(null)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setError(err instanceof Error ? err.message : 'Failed to load')
    }
  }, [api, db])

  useEffect(() => {
    void loadCounts()
  }, [loadCounts])

  // Switching database starts a fresh view.
  useEffect(() => {
    setItems(null)
    setStatus('')
    setQuery('')
    setShowSettled(false)
    setError(null)
    void loadItems()
  }, [loadItems])

  // The server says something changed: reload the list, the counts and any open item.
  useEffect(() => {
    if (!lastEvent) return
    void loadCounts()
    void loadItems()
    setRefreshKey((k) => k + 1)
  }, [lastEvent, loadCounts, loadItems])

  // Cross-database search, after a short pause in typing.
  useEffect(() => {
    const q = search.trim()
    if (q.length < 2) {
      setHits(null)
      return
    }
    const ctl = new AbortController()
    const t = setTimeout(() => {
      api.plannerSearch(q, undefined, ctl.signal).then(setHits).catch((err: unknown) => {
        if (!ctl.signal.aborted) notifyError(err, 'Search failed')
      })
    }, 250)
    return () => {
      clearTimeout(t)
      ctl.abort()
    }
  }, [api, search])

  const settledCount = useMemo(() => (items ?? []).filter((i) => isSettled(i.status)).length, [items])
  const statuses = useMemo(() => statusCounts(items ?? []), [items])
  const visible = useMemo(() => {
    const base = (items ?? []).filter((i) => status || showSettled || !isSettled(i.status))
    return filterItems(base, { status, query })
  }, [items, status, query, showSettled])
  const canReorder = !!items && visible.length === items.length && !status && !query.trim()

  const go = (nextDb: PlannerDb, id?: string) => navigate(id ? `/planner/${nextDb}/${encodeURIComponent(id)}` : `/planner/${nextDb}`)

  async function onDragEnd(e: DragEndEvent) {
    if (!items || !e.over || e.active.id === e.over.id) return
    const ids = moveId(items.map((i) => i.id), String(e.active.id), String(e.over.id))
    const byId = new Map(items.map((i) => [i.id, i]))
    setItems(ids.map((id) => byId.get(id)!)) // optimistic
    try {
      await api.plannerReorder(db, ids)
    } catch (err) {
      notifyError(err, 'Failed to reorder')
      void loadItems()
    }
  }

  async function enable() {
    try {
      await api.setSetting('features.planner', 'true')
      setEnabled(true)
      setError(null)
      void loadCounts()
      void loadItems()
    } catch (err) {
      notifyError(err, 'Failed to turn on the planner')
    }
  }

  if (enabled === false) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="Planner" />
        <div className="p-6">
          <EmptyState
            variant="empty"
            title="The planner is switched off"
            description="The planner holds priorities, workstreams, the roadmap, ideas, decisions, risks and an inbox. Turn it on to use it."
            action={{ label: 'Turn on planner', onClick: enable }}
          />
        </div>
      </div>
    )
  }

  const countOf = (key: PlannerDb) => counts.find((c) => c.name === key)?.count
  const maxRank = Math.max(0, ...(db === 'priorities' ? (items ?? []).map((i) => (typeof i.rank === 'number' ? i.rank : 0)) : [0]))

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Planner">
        <Input className="h-8 w-56" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search everything…" aria-label="Search the planner" />
        <Button size="sm" className="gap-1" onClick={() => setCreating(true)} disabled={enabled === null}>
          <Plus className="h-3.5 w-3.5" />
          New {meta.singular}
        </Button>
      </PageHeader>

      <div role="tablist" aria-label="Planner databases" className="flex gap-1 overflow-x-auto border-b border-border px-4">
        {PLANNER_DBS.map((d) => {
          const n = countOf(d.key)
          const active = d.key === db && hits === null
          return (
            <button
              key={d.key}
              type="button"
              role="tab"
              aria-selected={active}
              className={`whitespace-nowrap border-b-2 px-3 py-2 text-sm ${active ? 'border-primary font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}
              onClick={() => {
                setSearch('')
                go(d.key)
              }}
            >
              {d.label}
              {n !== undefined && <span className="ml-1.5 text-xs text-muted-foreground">{n}</span>}
            </button>
          )
        })}
      </div>

      <div className="flex-1 overflow-auto">
        {hits !== null ? (
          <SearchResults hits={hits} query={search.trim()} onClear={() => setSearch('')} onOpen={(h) => { setSearch(''); go(h.db, h.id) }} />
        ) : (
          <>
            <p className="px-4 pt-3 text-sm text-muted-foreground">{meta.blurb}</p>
            <div className="flex flex-wrap items-center gap-2 px-4 py-3">
              <Input className="h-8 w-48" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={`Filter ${meta.label.toLowerCase()}…`} aria-label={`Filter ${meta.label}`} />
              <button type="button" aria-pressed={status === ''} className={chip(status === '')} onClick={() => setStatus('')}>
                All
              </button>
              {statuses.map((s) => (
                <button key={s.status} type="button" aria-pressed={status === s.status} className={chip(status === s.status)} onClick={() => setStatus(status === s.status ? '' : s.status)}>
                  {s.status} <span className="text-muted-foreground">{s.count}</span>
                </button>
              ))}
              {settledCount > 0 && !status && (
                <button type="button" className="ml-auto text-xs text-primary hover:underline" onClick={() => setShowSettled((v) => !v)}>
                  {showSettled ? `Hide ${settledCount} settled` : `Show ${settledCount} settled`}
                </button>
              )}
            </div>

            {error ? (
              <div className="p-4">
                <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: () => void loadItems() }} />
              </div>
            ) : items === null ? (
              <div className="flex flex-col gap-2 p-4">
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
              </div>
            ) : items.length === 0 ? (
              <div className="p-4">
                <EmptyState variant="empty" title={`No ${meta.label.toLowerCase()} yet`} description={meta.blurb} action={{ label: `New ${meta.singular}`, onClick: () => setCreating(true) }} />
              </div>
            ) : visible.length === 0 ? (
              <p className="px-4 py-6 text-sm text-muted-foreground">Nothing matches. Clear the filter or show settled items.</p>
            ) : (
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
                <SortableContext items={visible.map((i) => i.id)} strategy={verticalListSortingStrategy} disabled={!canReorder}>
                  <ul aria-label={meta.label} className="border-t border-border">
                    {visible.map((it) => (
                      <PlannerRow key={it.id} db={db} item={it} sortable={canReorder} onOpen={() => go(db, it.id)} />
                    ))}
                  </ul>
                </SortableContext>
              </DndContext>
            )}
            {items && items.length > 1 && !canReorder && !error && (
              <p className="px-4 py-3 text-xs text-muted-foreground">Clear the filter and show settled items to drag-reorder.</p>
            )}
          </>
        )}
      </div>

      <PlannerCreateDialog
        db={db}
        open={creating}
        onOpenChange={setCreating}
        nextRank={maxRank + 1}
        onCreated={(it) => {
          void loadCounts()
          void loadItems()
          go(db, it.id)
        }}
      />

      {openId && (
        <PlannerDetailDialog
          key={`${db}/${openId}`}
          db={db}
          id={openId}
          refreshKey={refreshKey}
          onClose={() => go(db)}
          onChanged={() => {
            void loadCounts()
            void loadItems()
          }}
          onOpen={(nextDb, id) => go(nextDb, id)}
        />
      )}
    </div>
  )
}

function chip(active: boolean): string {
  return `rounded-full border px-2.5 py-0.5 text-xs ${active ? 'border-primary bg-primary/10 text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}`
}

function SearchResults({ hits, query, onClear, onOpen }: { hits: PlannerSearchHit[]; query: string; onClear: () => void; onOpen: (h: PlannerSearchHit) => void }) {
  return (
    <section aria-label="Search results">
      <div className="flex items-center justify-between px-4 py-3">
        <p className="text-sm">
          {hits.length} result{hits.length === 1 ? '' : 's'} for <span className="font-medium">“{query}”</span>
        </p>
        <Button size="sm" variant="ghost" className="gap-1" onClick={onClear}>
          <X className="h-3.5 w-3.5" /> Clear
        </Button>
      </div>
      {hits.length === 0 ? (
        <p className="px-4 pb-6 text-sm text-muted-foreground">Nothing matched. Every word has to appear in an item&apos;s id, title or text.</p>
      ) : (
        <ul className="border-t border-border">
          {hits.map((h) => (
            <li key={`${h.db}/${h.id}`} className="border-b border-border">
              <button type="button" className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/50" onClick={() => onOpen(h)} aria-label={`Open ${h.id}: ${h.title}`}>
                <span className="w-24 shrink-0 text-xs uppercase tracking-wide text-muted-foreground">{plannerMeta(h.db).label}</span>
                <span className="font-mono text-xs text-muted-foreground">{h.id}</span>
                <span className="min-w-0 flex-1 truncate font-medium">{h.title}</span>
                <PlannerStatus status={h.status} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
