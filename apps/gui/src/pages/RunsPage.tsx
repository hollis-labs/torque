import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, EmptyState, Skeleton, SummaryCards } from '@hollis-labs/sysop-ui'
import { RunCard } from '@/components/domain/run-card'
import { useApi } from '@/hooks/use-api'
import { usePagedList, type ListChange, type PageRequest } from '@/hooks/use-paged-list'
import type { RunFacetResult, RunQuery } from '@/lib/api'
import type { Run } from '@/lib/types'

const PAGE_SIZE = 50
const inputClass = 'h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground'
type Cohort = Pick<RunQuery, 'status' | 'executor' | 'profile' | 'since' | 'until'>
type PageParams = Cohort & Pick<RunQuery, 'sort_by' | 'sort_dir' | 'include_total'> & { limit: number }
type WindowChoice = 'all' | '24h' | '7d' | '30d'

export default function RunsPage() {
  const api = useApi()
  const [status, setStatus] = useState('all')
  const [executor, setExecutor] = useState('')
  const [profile, setProfile] = useState('')
  const [windowChoice, setWindowChoice] = useState<WindowChoice>('all')
  const [cohort, setCohort] = useState<Cohort>({})
  const [sortBy, setSortBy] = useState<'started_at' | 'duration' | 'cost'>('started_at')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [includeTotal, setIncludeTotal] = useState(false)
  const [sseStatus, setSseStatus] = useState('connecting')
  const [facetRevision, setFacetRevision] = useState(0)
  const [facetState, setFacetState] = useState<{ key: string; result?: RunFacetResult; error?: string } | null>(null)
  const scrollRoot = useRef<HTMLDivElement>(null)
  const sentinel = useRef<HTMLDivElement>(null)
  const loadedRows = useRef<Run[]>([])
  const cohortKey = JSON.stringify(cohort)
  const facets = facetState?.key === cohortKey ? facetState : null
  const invalidateFacets = useCallback(() => setFacetRevision((n) => n + 1), [])

  useEffect(() => {
    const abort = new AbortController()
    api.runFacets(cohort, 'status,executor,profile', abort.signal).then((result) => {
      if (!abort.signal.aborted) setFacetState({ key: cohortKey, result })
    }).catch((err: unknown) => {
      if (!abort.signal.aborted) setFacetState({ key: cohortKey, error: err instanceof Error ? err.message : 'Failed to load run totals' })
    })
    return () => abort.abort()
  }, [api, cohort, cohortKey, facetRevision])

  const fetchPage = useCallback(({ params, cursor, signal }: PageRequest<PageParams>) => api.pageRuns({ ...params, ...(cursor ? { cursor } : {}) }, signal), [api])
  const subscribe = useCallback((onChange: (change: ListChange<Run>) => void) => api.subscribeEvents((event) => {
    if (event.type !== 'run.started' && event.type !== 'run.completed' && event.type !== 'task.updated' && event.type !== 'task.transitioned') return
    // Membership and sorting stay on the server. A changed loaded run is marked
    // stale by the hook until Refresh obtains a new server page; never filter it
    // by executor/profile/status in the browser or synthesize new rows from SSE.
    if (typeof event.data.run_id === 'number') {
      onChange({ id: event.data.run_id, refresh: true })
    } else {
      // Task selector/scope edits can change the membership of existing runs.
      // Identify affected loaded IDs without deciding whether they still match.
      for (const run of loadedRows.current) {
        if (run.task_id === event.data.task_id) onChange({ id: run.id, refresh: true })
      }
      onChange({ id: -1 })
    }
  }, setSseStatus), [api])
  const params = useMemo<PageParams>(() => ({ ...cohort, limit: PAGE_SIZE, sort_by: sortBy, sort_dir: sortDir, ...(includeTotal ? { include_total: true } : {}) }), [cohort, sortBy, sortDir, includeTotal])
  const list = usePagedList<Run, PageParams>({ fetchPage, params, getId: (run) => run.id, subscribe, onInvalidate: invalidateFacets })
  const { items, hasMore, loading, error, isStale, loadMore } = list

  useEffect(() => { loadedRows.current = items }, [items])

  useEffect(() => {
    if (!hasMore || loading || error || isStale || !sentinel.current) return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMore()
    }, { root: scrollRoot.current, rootMargin: '200px' })
    observer.observe(sentinel.current)
    return () => observer.disconnect()
  }, [hasMore, loading, error, isStale, loadMore])

  function applyFilters() {
    const next: Cohort = {}
    if (status !== 'all') next.status = status
    if (executor.trim()) next.executor = executor.trim()
    if (profile.trim()) next.profile = profile.trim()
    if (windowChoice !== 'all') {
      const now = new Date()
      const hours = windowChoice === '24h' ? 24 : windowChoice === '7d' ? 7 * 24 : 30 * 24
      next.since = new Date(now.getTime() - hours * 60 * 60 * 1000).toISOString()
      next.until = now.toISOString()
    }
    setCohort(next)
  }

  function refresh() {
    void list.reload()
    invalidateFacets()
  }
  const statusFacet = facets?.result?.facets.find((facet) => facet.dimension === 'status')
  const counts = new Map(statusFacet?.buckets.map((bucket) => [String(bucket.value), bucket.count]))
  const cards = facets?.result ? [
    { label: 'Matching', value: facets.result.matching_count },
    { label: 'Running', value: counts.get('running') ?? 0 },
    { label: 'Completed', value: (counts.get('done') ?? 0) + (counts.get('completed') ?? 0) },
    { label: 'Failed', value: counts.get('failed') ?? 0 },
  ] : []
  const statusChoices = [...new Set(['running', 'done', 'failed', ...(statusFacet?.buckets.map((bucket) => String(bucket.value)) ?? [])])]
  const choices = (dimension: string) => facets?.result?.facets.find((facet) => facet.dimension === dimension)?.buckets.flatMap((bucket) => typeof bucket.value === 'string' && bucket.value ? [bucket.value] : []) ?? []

  return <div className="flex h-full flex-col">
    <div className="flex items-center justify-between gap-3 border-b border-border bg-card px-6 py-4">
      <div className="flex items-center gap-3"><h1 className="text-lg font-semibold">Runs</h1><span className="text-xs text-muted-foreground">{sseStatus === 'live' ? 'Live' : 'Offline'}</span></div>
      <Button size="sm" variant="outline" onClick={refresh} disabled={loading}>Refresh</Button>
    </div>
    {facets?.result && <><SummaryCards cards={cards} /><p className="px-6 pt-2 font-mono text-xs text-muted-foreground">{(facets.result.totals.prompt_tokens + facets.result.totals.completion_tokens).toLocaleString()} tokens · ${facets.result.totals.cost.toFixed(2)} ledger cost</p></>}
    {!facets && <Skeleton className="mx-6 mt-2 h-7" />}
    {facets?.error && <p role="alert" className="px-6 pt-2 text-sm text-destructive">Run totals unavailable: {facets.error}</p>}
    <form onSubmit={(event) => { event.preventDefault(); applyFilters() }} className="flex flex-wrap items-end gap-3 border-b border-border px-6 py-4">
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Status<select aria-label="Status" className={inputClass} value={status} onChange={(event) => setStatus(event.target.value)}><option value="all">All statuses</option>{statusChoices.map((value) => <option key={value} value={value}>{value === 'done' ? 'Completed' : value}</option>)}</select></label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Executors<input aria-label="Executors" className={`${inputClass} w-36`} list="run-executors" placeholder="All executors" value={executor} onChange={(event) => setExecutor(event.target.value)} /><datalist id="run-executors">{choices('executor').map((value) => <option key={value} value={value} />)}</datalist></label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Current task profiles<input aria-label="Current task profiles" className={`${inputClass} w-40`} list="run-profiles" placeholder="All profiles" value={profile} onChange={(event) => setProfile(event.target.value)} /><datalist id="run-profiles">{choices('profile').map((value) => <option key={value} value={value} />)}</datalist></label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Run start window<select aria-label="Run start window" className={inputClass} value={windowChoice} onChange={(event) => setWindowChoice(event.target.value as WindowChoice)}><option value="all">All time</option><option value="24h">Last 24 hours</option><option value="7d">Last 7 days</option><option value="30d">Last 30 days</option></select></label>
      <Button type="submit" size="sm">Apply filters</Button>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Sort<select aria-label="Sort" className={inputClass} value={sortBy} onChange={(event) => setSortBy(event.target.value as typeof sortBy)}><option value="started_at">Started</option><option value="duration">Duration</option><option value="cost">Cost</option></select></label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">Direction<select aria-label="Direction" className={inputClass} value={sortDir} onChange={(event) => setSortDir(event.target.value as typeof sortDir)}><option value="desc">Descending</option><option value="asc">Ascending</option></select></label>
      <label className="flex h-8 items-center gap-2 text-xs text-muted-foreground"><input type="checkbox" checked={includeTotal} onChange={(event) => setIncludeTotal(event.target.checked)} />Show total progress</label>
      <p className="w-full text-xs text-muted-foreground">Separate multiple executors or profiles with commas. Profile means the task’s current launch profile, with agent profile as fallback.{cohort.until && ` Window ends ${new Date(cohort.until).toLocaleString()}.`}</p>
    </form>
    {isStale && <p role="status" className="px-6 pt-3 text-sm text-muted-foreground">Loaded runs changed. Refresh to update this page.</p>}
    <div ref={scrollRoot} className="flex-1 overflow-auto p-6">
      {loading && !items.length ? <div className="flex flex-col gap-3">{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-28 rounded-lg" />)}</div> : <>
        {error && <EmptyState variant="error" title="Could not load runs" description={error.message} action={{ label: 'Retry', onClick: refresh }} />}
        {!error && !items.length && <EmptyState variant="no-results" title="No matching runs" description="Try another status, executor, profile or date window." />}
        <div className="flex max-w-2xl flex-col gap-3">{items.map((run) => <RunCard key={run.id} run={run} showTaskLink />)}</div>
        <p className="mt-4 text-xs text-muted-foreground">{items.length.toLocaleString()} loaded{list.total !== undefined && ` of ${list.total.toLocaleString()}`}</p>
        {hasMore && !error && <div ref={sentinel} className="py-4"><Button variant="outline" size="sm" disabled={loading || isStale} onClick={() => void loadMore()}>{list.loadingMore ? 'Loading…' : 'Load older runs'}</Button></div>}
      </>}
    </div>
  </div>
}
