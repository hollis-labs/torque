import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Plus, X } from 'lucide-react'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { Button, EmptyState, Input, PageHeader, Skeleton, Tabs, TabsList, TabsTrigger } from '@hollis-labs/sysop-ui'
import { PlannerBoardView } from '@/components/planner/planner-board'
import { PlannerCreateDialog } from '@/components/planner/planner-create-dialog'
import { PlannerDetailDialog } from '@/components/planner/planner-detail-dialog'
import { PlannerRow } from '@/components/planner/planner-row'
import { TorqueTaskDialog } from '@/components/planner/torque-task-dialog'
import { PlannerStatus } from '@/components/planner/planner-status'
import { AgentActivityView } from '@/components/planner/agent-activity'
import { AgentBuilderView } from '@/components/planner/agent-builder'
import { FleetAgentsPanel } from '@/components/planner/fleet-agents'
import { useAgentActivity } from '@/hooks/use-agent-activity'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { ApiError } from '@/lib/api'
import { PLANNER_DBS, filterItems, isPlannerDb, isSettled, moveId, parseBoardOpen, plannerMeta, statusCounts } from '@/lib/planner'
import { notifyError } from '@/lib/toast'
import type { PlannerBoard, PlannerDatabaseInfo, PlannerDb, PlannerItem, PlannerSearchHit } from '@/lib/types'

const SSE_EVENTS = ['planner.changed', 'task.created', 'task.updated', 'task.transitioned']
/** The board is a live view: it also refreshes on its own while open. */
const BOARD_REFRESH_MS = 30_000

export default function PlannerPage() {
  const api = useApi()
  const navigate = useNavigate()
  const params = useParams<{ db?: string; id?: string }>()
  const [searchParams, setSearchParams] = useSearchParams()
  // /planner and /planner/board are the curated board, /planner/activity the
  // activity feed (scope=all); /planner/<db> is a list.
  const isActivity = params.db === 'activity'
  const isAgents = params.db === 'agents' // live agent monitor (relayed by /api/v1/activity)
  const isAgentsDemo = params.db === 'agents-demo' // the leaderboard design spike, demo data; no tab
  const isBuilder = params.db === 'builder' // agent builder design spike (demo data)
  const isDemo = isAgents || isAgentsDemo || isBuilder
  const isBoard = params.db === undefined || params.db === 'board' || isActivity
  const boardScope = isActivity ? 'all' : 'curated'
  const boardBase = isActivity ? '/planner/activity' : '/planner/board'
  const db: PlannerDb = isPlannerDb(params.db) ? params.db : 'priorities'
  const openId = params.id ? decodeURIComponent(params.id) : undefined
  const boardOpen = isBoard ? parseBoardOpen(searchParams) : {} // the board opens items in modals over itself
  const meta = plannerMeta(db)
  const createDb: PlannerDb = isBoard ? 'inbox' : db // the board's New button adds to the inbox
  const createMeta = plannerMeta(createDb)

  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [counts, setCounts] = useState<PlannerDatabaseInfo[]>([])
  const [board, setBoard] = useState<PlannerBoard | null>(null)
  const [boardError, setBoardError] = useState<string | null>(null)
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
  const activity = useAgentActivity()

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

  const loadBoard = useCallback(async () => {
    try {
      setBoard(await api.plannerBoard({ scope: boardScope }))
      setBoardError(null)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setBoardError(err instanceof Error ? err.message : 'Failed to load the board')
    }
  }, [api, boardScope])

  useEffect(() => {
    void loadCounts()
  }, [loadCounts])

  // The board loads when opened and then keeps itself fresh.
  useEffect(() => {
    if (!isBoard) return
    setBoard(null) // the curated board and the activity feed are different documents
    void loadBoard()
    const t = setInterval(() => void loadBoard(), BOARD_REFRESH_MS)
    return () => clearInterval(t)
  }, [isBoard, loadBoard])

  // Switching database starts a fresh view.
  useEffect(() => {
    setItems(null)
    setStatus('')
    setQuery('')
    setShowSettled(false)
    setError(null)
    if (!isBoard && !isDemo) void loadItems()
  }, [loadItems, isBoard, isDemo])

  // The server says something changed: reload the list, the counts and any open item.
  useEffect(() => {
    if (!lastEvent) return
    if (isBoard) void loadBoard()
    if (lastEvent.type.startsWith('task.')) {
      setRefreshKey((k) => k + 1) // an open workstream or reference may show this task
      return
    }
    void loadCounts()
    if (!isBoard && !isDemo) void loadItems()
    setRefreshKey((k) => k + 1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastEvent])

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
      if (isBoard) void loadBoard()
      else void loadItems()
    } catch (err) {
      notifyError(err, 'Failed to turn on the planner')
    }
  }

  if (enabled === false) {
    return (
      <div className="flex h-full min-w-0 flex-col">
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
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Planner">
        {!isDemo && <Button size="sm" className="gap-1" onClick={() => setCreating(true)} disabled={enabled === null}>
          <Plus className="h-3.5 w-3.5" />
          New {createMeta.singular}
        </Button>}
      </PageHeader>

      <div className="border-b border-zinc-800/80 px-4 py-3">
        <Input className="h-8 w-full sm:w-72" value={search} onChange={e => setSearch(e.target.value)} placeholder="Search everything…" aria-label="Search the planner" />
      </div>
      <Tabs value={isActivity ? 'activity' : isBoard ? 'board' : isAgents ? 'agents' : isAgentsDemo ? 'agents-demo' : isBuilder ? 'builder' : db} onValueChange={value => { setSearch(''); navigate(`/planner/${value}`) }} className="min-w-0 border-b border-zinc-800/80 px-4 py-3">
        <TabsList aria-label="Planner views" className="h-auto max-w-full flex-wrap justify-start">
          <TabsTrigger value="board">Board</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
          {PLANNER_DBS.map(d => <TabsTrigger key={d.key} value={d.key}>
            {d.label}{countOf(d.key) !== undefined && <span className="ml-1.5 text-[10px] text-zinc-500">{countOf(d.key)}</span>}
          </TabsTrigger>)}
          <TabsTrigger value="agents">Agents</TabsTrigger>
          <TabsTrigger value="builder">Builder</TabsTrigger>
        </TabsList>
      </Tabs>

      <div className="flex-1 overflow-auto">
        {hits !== null ? (
          <SearchResults hits={hits} query={search.trim()} onClear={() => setSearch('')} onOpen={(h) => { setSearch(''); go(h.db, h.id) }} />
        ) : isAgents ? (
          <FleetAgentsPanel />
        ) : isAgentsDemo ? (
          <AgentActivityView agents={activity.agents} now={activity.now} live={activity.live} />
        ) : isBuilder ? (
          <AgentBuilderView />
        ) : isBoard ? (
          boardError ? (
            <div className="p-4">
              <EmptyState variant="error" title="Something went wrong" description={boardError} action={{ label: 'Retry', onClick: () => void loadBoard() }} />
            </div>
          ) : board === null ? (
            <div className="flex flex-col divide-y divide-zinc-800/80">
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
            </div>
          ) : (
            <PlannerBoardView board={board} scope={boardScope} base={boardBase} />
          )
        ) : (
          <>
            <p className="px-4 pt-3 text-[13px] text-muted-foreground">{meta.blurb}</p>
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
              <p className="px-4 py-6 text-[13px] text-muted-foreground">Nothing matches. Clear the filter or show settled items.</p>
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
        db={createDb}
        open={creating}
        onOpenChange={setCreating}
        nextRank={maxRank + 1}
        onCreated={(it) => {
          void loadCounts()
          if (isBoard) void loadBoard()
          else void loadItems()
          go(createDb, it.id)
        }}
      />

      {boardOpen.item && (
        <PlannerDetailDialog
          key={`board/${boardOpen.item.db}/${boardOpen.item.id}`}
          db={boardOpen.item.db}
          id={boardOpen.item.id}
          refreshKey={refreshKey}
          onClose={() => setSearchParams({})}
          onChanged={() => {
            void loadCounts()
            void loadBoard()
          }}
          onOpen={(nextDb, id) => navigate(`${boardBase}?open=${nextDb}/${encodeURIComponent(id)}`)}
        />
      )}

      {boardOpen.task && !boardOpen.item && <TorqueTaskDialog key={boardOpen.task} id={boardOpen.task} onClose={() => setSearchParams({})} />}

      {openId && !isBoard && (
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
  return `rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider ${active ? 'border-primary bg-primary/10 text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}`
}

function SearchResults({ hits, query, onClear, onOpen }: { hits: PlannerSearchHit[]; query: string; onClear: () => void; onOpen: (h: PlannerSearchHit) => void }) {
  return (
    <section aria-label="Search results">
      <div className="flex min-w-0 items-center justify-between gap-3 px-4 py-3 [overflow-wrap:anywhere]">
        <p className="text-[13px]">
          {hits.length} result{hits.length === 1 ? '' : 's'} for <span className="font-medium">“{query}”</span>
        </p>
        <Button size="sm" variant="ghost" className="gap-1" onClick={onClear}>
          <X className="h-3.5 w-3.5" /> Clear
        </Button>
      </div>
      {hits.length === 0 ? (
        <p className="px-4 pb-6 text-[13px] text-muted-foreground">Nothing matched. Every word has to appear in an item&apos;s id, title or text.</p>
      ) : (
        <ul className="border-t border-border">
          {hits.map((h) => (
            <li key={`${h.db}/${h.id}`} className="border-b border-border">
              <button type="button" className="flex w-full flex-wrap items-center gap-3 px-4 py-3 text-left hover:bg-muted/50" onClick={() => onOpen(h)} aria-label={`Open ${h.id}: ${h.title}`}>
                <span className="w-24 shrink-0 text-xs uppercase tracking-wide text-muted-foreground">{plannerMeta(h.db).label}</span>
                <span className="font-mono text-xs text-muted-foreground">{h.id}</span>
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere] text-[13px] font-medium">{h.title}</span>
                <PlannerStatus status={h.status} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
