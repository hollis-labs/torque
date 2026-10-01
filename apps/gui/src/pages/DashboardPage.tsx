import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Card, CardContent, Skeleton, Tabs, TabsContent, TabsList, TabsTrigger, PageHeader, SummaryCards, EmptyState, Button } from '@hollis-labs/sysop-ui'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { RecentRuns } from '@/components/widgets'
import { AggregateBreakdown, AggregateCost, AggregateRunHeatmap, AggregateRunHistory, AggregateRunPulse, AggregateRunStatus, AggregateTaskPipeline, AggregateTokens } from '@/components/widgets/run-aggregate-charts'
import { useApi } from '@/hooks/use-api'
import { useDebouncedCallback } from '@/hooks/use-debounced-callback'
import { EVENT_REFETCH_DEBOUNCE_MS } from '@/lib/debounce'
import { STATUS_COLOR_VAR } from '@/lib/constants'
import { cn } from '@/lib/utils'
import type { Run, SSEEvent, TaskScopeRollupResponse } from '@/lib/types'
import type { RunFacetResult, RunTimeSeries, TaskFacetResult, TorqueApiClient } from '@/lib/api'

type DashboardTab = 'activity' | 'mission-control' | 'usage'
const TAB_VALUES: DashboardTab[] = ['activity', 'mission-control', 'usage']
const RECENT_PAGE_SIZE = 12
const STALE_MS = 5 * 60 * 1000
const LIVE_EVENT_TYPES = new Set(['run.started', 'run.completed', 'task.created', 'task.updated', 'task.transitioned'])

type DashboardAggregates = {
  tasks: TaskFacetResult
  projects: TaskScopeRollupResponse
  runs: RunFacetResult
  history: RunTimeSeries
  activity: RunTimeSeries
  pulse: RunTimeSeries
}

async function loadAggregates(api: TorqueApiClient): Promise<DashboardAggregates> {
  const now = new Date()
  const until = now.toISOString()
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()))
  const historyStart = new Date(today)
  historyStart.setUTCDate(today.getUTCDate() - 13)
  const activityStart = new Date(today)
  activityStart.setUTCDate(today.getUTCDate() - today.getUTCDay() - 15 * 7)
  const [tasks, projects, runs, history, activity, pulse] = await Promise.all([
    api.taskFacets(undefined, 'status'),
    api.taskRollup('project_id'),
    api.runFacets({ since: historyStart.toISOString(), until }),
    api.runTimeSeries({ bucket: 'day', since: historyStart.toISOString(), until }),
    api.runTimeSeries({ bucket: 'day', since: activityStart.toISOString(), until }),
    api.runTimeSeries({ bucket: 'hour', since: new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString(), until }),
  ])
  return { tasks, projects, runs, history, activity, pulse }
}

function applyRunEvent(runs: Run[], ev: SSEEvent): Run[] {
  if (ev.type !== 'run.completed') return runs
  const id = ev.data.run_id
  const payload = ev.data.payload && typeof ev.data.payload === 'object' ? ev.data.payload as Record<string, unknown> : {}
  return runs.map((run) => run.id !== id ? run : {
    ...run,
    status: typeof payload.status === 'string' ? payload.status : 'done',
    completed_at: new Date().toISOString(),
    ...(typeof payload.cost === 'number' ? { cost: payload.cost } : {}),
  })
}

export default function DashboardPage() {
  const api = useApi()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<DashboardAggregates | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [olderPage, setOlderPage] = useState(false)
  const [rowsLoading, setRowsLoading] = useState(true)
  const [rowsError, setRowsError] = useState<string | null>(null)
  const [sseStatus, setSseStatus] = useState('connecting')
  const lifecycle = useRef({ active: false, aggregateRequest: 0, rowRequest: 0, lastFetched: 0 })

  const activeTab = useMemo<DashboardTab>(() => {
    const value = searchParams.get('tab') as DashboardTab | null
    return value && TAB_VALUES.includes(value) ? value : 'activity'
  }, [searchParams])

  const refreshAggregates = useCallback(async () => {
    const state = lifecycle.current
    const request = ++state.aggregateRequest
    try {
      const result = await loadAggregates(api)
      if (!state.active || request !== state.aggregateRequest) return
      setData(result)
      setError(null)
      state.lastFetched = Date.now()
    } catch (err) {
      if (state.active && request === state.aggregateRequest) setError(err instanceof Error ? err.message : 'Failed to refresh dashboard')
    }
  }, [api])

  const loadRecentPage = useCallback(async (cursor?: string | null) => {
    const state = lifecycle.current
    const request = ++state.rowRequest
    try {
      const page = await api.pageRuns({ limit: RECENT_PAGE_SIZE, sort_by: 'started_at', sort_dir: 'desc', ...(cursor ? { cursor } : {}) })
      if (!state.active || request !== state.rowRequest) return
      setRuns(page.items)
      setNextCursor(page.meta.next_cursor)
      setOlderPage(!!cursor)
      setRowsError(null)
    } catch (err) {
      if (state.active && request === state.rowRequest) setRowsError(err instanceof Error ? err.message : 'Failed to load recent runs')
    } finally {
      if (state.active && request === state.rowRequest) setRowsLoading(false)
    }
  }, [api])

  useEffect(() => {
    const state = lifecycle.current
    state.active = true
    void refreshAggregates()
    void loadRecentPage()
    return () => {
      state.active = false
      state.aggregateRequest++
      state.rowRequest++
    }
  }, [refreshAggregates, loadRecentPage])

  const scheduleAggregateRefresh = useDebouncedCallback(() => void refreshAggregates(), EVENT_REFETCH_DEBOUNCE_MS)

  // Keep row membership stable while browsing. No event buffer is needed:
  // pulse/history come from SQL buckets, and only loaded rows are patched.
  useEffect(() => api.subscribeEvents((event) => {
    if (!LIVE_EVENT_TYPES.has(event.type)) return
    scheduleAggregateRefresh()
    if (event.type === 'run.completed') setRuns((prev) => applyRunEvent(prev, event))
  }, setSseStatus), [api, scheduleAggregateRefresh])

  // Stale tab/timer refreshes aggregate data only, never the recent pages.
  useEffect(() => {
    if (lifecycle.current.lastFetched > 0 && Date.now() - lifecycle.current.lastFetched >= STALE_MS) scheduleAggregateRefresh()
  }, [activeTab, scheduleAggregateRefresh])
  useEffect(() => {
    const timer = setInterval(scheduleAggregateRefresh, STALE_MS)
    return () => clearInterval(timer)
  }, [scheduleAggregateRefresh])

  const taskStatus = data?.tasks.facets.find((f) => f.dimension === 'status')
  const counts = new Map(taskStatus?.buckets.map((b) => [String(b.value), b.count]))
  const total = data?.tasks.matching_count ?? 0
  const cards = [
    { label: 'Total', value: total },
    { label: 'Active', value: total - (counts.get('done') ?? 0) - (counts.get('archived') ?? 0), accentColor: STATUS_COLOR_VAR.doing },
    { label: 'Done', value: counts.get('done') ?? 0, accentColor: STATUS_COLOR_VAR.done },
  ]

  function selectTab(next: string) {
    if (!TAB_VALUES.includes(next as DashboardTab)) return
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === 'activity') params.delete('tab')
      else params.set('tab', next)
      return params
    }, { replace: true })
  }

  function changeRecentPage(cursor?: string | null) {
    setRowsLoading(true)
    void loadRecentPage(cursor)
  }

  return <div className="flex h-full flex-col">
    <PageHeader title="Ops Dashboard"><SseStatusPill status={sseStatus} /><RestartFrontendButton /></PageHeader>
    {data && <SummaryCards cards={cards} />}
    <div className="flex-1 overflow-auto">
      {!data ? error ? <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: () => void refreshAggregates() }} /> : <DashboardSkeleton /> : <>
        {error && <p role="status" className="px-4 pt-3 text-sm text-destructive">{error} <Button size="sm" onClick={() => void refreshAggregates()}>Retry</Button></p>}
        <Tabs value={activeTab} onValueChange={selectTab} className="gap-0">
          <TabsList className="mx-4 mt-3"><TabsTrigger value="activity">Activity</TabsTrigger><TabsTrigger value="mission-control">Mission Control</TabsTrigger><TabsTrigger value="usage">Usage</TabsTrigger></TabsList>
          <TabsContent value="activity" keepMounted className="px-4 py-4">
            <div className="flex flex-col gap-4">
              <Card size="sm"><CardContent><AggregateRunHeatmap series={data.activity} /></CardContent></Card>
              <div className="grid grid-cols-1 gap-4 xl:grid-cols-[1fr_minmax(320px,420px)]">
                <Card size="sm"><CardContent><AggregateRunPulse series={data.pulse} /></CardContent></Card>
                <Card size="sm"><CardContent>
                  {rowsLoading ? <Skeleton className="h-48" /> : <RecentRuns runs={runs} limit={RECENT_PAGE_SIZE} />}
                  {rowsError && <p role="alert" className="text-sm text-destructive">{rowsError}</p>}
                  <div className="mt-3 flex justify-between gap-2">
                    <Button size="sm" variant="outline" disabled={rowsLoading} onClick={() => changeRecentPage()}>{olderPage ? 'Newest runs' : 'Refresh runs'}</Button>
                    <Button size="sm" variant="outline" disabled={rowsLoading || !nextCursor} onClick={() => changeRecentPage(nextCursor)}>Older runs</Button>
                  </div>
                </CardContent></Card>
              </div>
            </div>
          </TabsContent>
          <TabsContent value="mission-control" keepMounted className="px-4 py-4">
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-[1fr_minmax(260px,340px)]">
              <div className="flex flex-col gap-4">
                <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_minmax(240px,320px)]">
                  <Card size="sm"><CardContent><AggregateRunHistory series={data.history} /></CardContent></Card>
                  <Card size="sm"><CardContent><AggregateRunStatus facet={data.runs.facets.find((f) => f.dimension === 'status')} total={data.runs.matching_count} /></CardContent></Card>
                </div>
                <Card size="sm"><CardContent><AggregateTokens series={data.history} /></CardContent></Card>
              </div>
              <Card size="sm"><CardContent>
                <AggregateTaskPipeline facet={taskStatus} total={total} />
                <p className="mt-3 font-mono text-[10px] text-muted-foreground">{data.projects.total.toLocaleString()} tasks assigned across {data.projects.scopes.length.toLocaleString()} projects</p>
              </CardContent></Card>
            </div>
          </TabsContent>
          <TabsContent value="usage" keepMounted className="px-4 py-4">
            <div className="flex flex-col gap-4">
              <Card size="sm"><CardContent><AggregateCost series={data.history} /></CardContent></Card>
              <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
                <Card size="sm"><CardContent><AggregateBreakdown facet={data.runs.facets.find((f) => f.dimension === 'executor')} total={data.runs.matching_count} title="Executors — 14d" /></CardContent></Card>
                <Card size="sm"><CardContent><AggregateBreakdown facet={data.runs.facets.find((f) => f.dimension === 'profile')} total={data.runs.matching_count} title="Current task profiles — 14d" /></CardContent></Card>
              </div>
            </div>
          </TabsContent>
        </Tabs>
      </>}
    </div>
  </div>
}

function DashboardSkeleton() {
  return <div className="flex flex-col gap-4 p-4"><Skeleton className="h-8 w-40" /><div className="grid grid-cols-3 gap-3">{Array.from({ length: 3 }, (_, i) => <Skeleton key={i} className="h-20 rounded-md" />)}</div><Skeleton className="h-48 rounded-md" /></div>
}

function SseStatusPill({ status }: { status: string }) {
  const live = status === 'live'
  return <span className={cn('flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.18em]', live ? 'text-emerald-400' : status === 'error' ? 'text-red-400' : 'text-zinc-500')} aria-live="polite">
    <span className={cn('h-1.5 w-1.5 rounded-full', live ? 'animate-pulse bg-emerald-400' : status === 'error' ? 'bg-red-500' : 'bg-zinc-600')} />{live ? 'live' : status}
  </span>
}
