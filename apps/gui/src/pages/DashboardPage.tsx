import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Card, CardContent, Skeleton, Tabs, TabsContent, TabsList, TabsTrigger, PageHeader, SummaryCards, EmptyState } from '@hollis-labs/sysop-ui'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import {
  ActivityHeatmap,
  CostPerDay,
  Pulse24h,
  RecentRuns,
  RunStatusDistribution,
  RunsChart,
  TaskPipeline,
  TokenThroughput,
} from '@/components/widgets'
import { useApi } from '@/hooks/use-api'
import { STATUS_COLOR_VAR } from '@/lib/constants'
import { cn } from '@/lib/utils'
import type { Run, SSEEvent, Task } from '@/lib/types'

type DashboardTab = 'activity' | 'mission-control' | 'usage'
const TAB_VALUES: DashboardTab[] = ['activity', 'mission-control', 'usage']

function isDashboardTab(v: string | null): v is DashboardTab {
  return !!v && (TAB_VALUES as string[]).includes(v)
}

type SseStatus = 'connecting' | 'live' | 'error'

const LIVE_EVENT_TYPES = [
  'run.started',
  'run.completed',
  'task.created',
  'task.updated',
  'task.transitioned',
] as const
const LIVE_EVENT_SET = new Set<string>(LIVE_EVENT_TYPES)

const EVENT_BUFFER_CAP = 500
const RUNS_LIMIT = 500
const TASKS_LIMIT = 500
const STALE_MS = 5 * 60 * 1000

function DashboardSkeleton() {
  return (
    <div className="flex flex-col gap-4 p-4">
      <Skeleton className="h-8 w-40" />
      <div className="grid grid-cols-3 gap-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-20 rounded-md" />
        ))}
      </div>
      <Skeleton className="h-48 rounded-md" />
    </div>
  )
}

/**
 * Synthesize a Run-shaped row from an SSE run.started payload so widgets
 * fed by the aggregate runs list start reflecting live activity before the
 * next full refetch. run.completed just carries status/cost updates, which
 * we splice into the existing row.
 */
function synthRunFromStartEvent(ev: SSEEvent): Run | null {
  const data = ev.data
  const runId = typeof data.run_id === 'number' ? data.run_id : null
  const taskId = typeof data.task_id === 'string' ? data.task_id : null
  if (!runId || !taskId) return null
  const payload =
    (data.payload && typeof data.payload === 'object' ? (data.payload as Record<string, unknown>) : {}) ?? {}
  const executor = typeof payload.executor === 'string' ? payload.executor : ''
  const startedAt =
    typeof payload.started_at === 'string' && payload.started_at
      ? payload.started_at
      : new Date().toISOString()
  return {
    id: runId,
    task_id: taskId,
    executor,
    agent_profile: '',
    status: 'running',
    prompt_tokens: 0,
    completion_tokens: 0,
    cost: 0,
    exit_code: 0,
    error_message: '',
    started_at: startedAt,
    completed_at: null,
  }
}

function applyRunEvent(runs: Run[], ev: SSEEvent): Run[] {
  const runId = typeof ev.data.run_id === 'number' ? ev.data.run_id : null
  if (!runId) return runs
  if (ev.type === 'run.started') {
    if (runs.some((r) => r.id === runId)) return runs
    const synth = synthRunFromStartEvent(ev)
    if (!synth) return runs
    return [synth, ...runs].slice(0, RUNS_LIMIT)
  }
  if (ev.type === 'run.completed') {
    const payload =
      (ev.data.payload && typeof ev.data.payload === 'object'
        ? (ev.data.payload as Record<string, unknown>)
        : {}) ?? {}
    const status = typeof payload.status === 'string' ? payload.status : 'completed'
    const cost = typeof payload.cost === 'number' ? payload.cost : undefined
    const idx = runs.findIndex((r) => r.id === runId)
    if (idx === -1) return runs
    const completedAt = new Date().toISOString()
    const next = runs.slice()
    next[idx] = {
      ...next[idx],
      status,
      completed_at: completedAt,
      ...(cost !== undefined ? { cost } : {}),
    }
    return next
  }
  return runs
}

export default function DashboardPage() {
  const api = useApi()
  const [searchParams, setSearchParams] = useSearchParams()

  const [tasks, setTasks] = useState<Task[]>([])
  const [runs, setRuns] = useState<Run[]>([])
  const [events, setEvents] = useState<SSEEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [sseStatus, setSseStatus] = useState<SseStatus>('connecting')

  const lastFetchedRef = useRef<number>(0)

  const activeTab: DashboardTab = useMemo(() => {
    const raw = searchParams.get('tab')
    return isDashboardTab(raw) ? raw : 'activity'
  }, [searchParams])

  useEffect(() => {
    let cancelled = false
    Promise.all([api.listTasks({ limit: TASKS_LIMIT }), api.listAllRuns({ limit: RUNS_LIMIT })])
      .then(([tasksRes, runsRes]) => {
        if (cancelled) return
        setTasks(tasksRes.tasks)
        setRuns(runsRes)
        lastFetchedRef.current = Date.now()
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api])

  // SSE subscription — single stream feeds both the event buffer (Pulse)
  // and run state updates. Filters to the five Ops-relevant types.
  useEffect(() => {
    const unsubscribe = api.subscribeEvents(
      (ev) => {
        if (!LIVE_EVENT_SET.has(ev.type)) return
        setEvents((prev) => {
          const next = prev.length >= EVENT_BUFFER_CAP ? prev.slice(1) : prev
          return [...next, ev]
        })
        if (ev.type === 'run.started' || ev.type === 'run.completed') {
          setRuns((prev) => applyRunEvent(prev, ev))
        }
      },
      (status) => setSseStatus(status)
    )
    return unsubscribe
  }, [api])

  // Stale-data refresh — on tab change, refetch only when >5min since the
  // last successful load. Widgets keep rendering stale data in the meantime.
  useEffect(() => {
    if (loading) return
    if (Date.now() - lastFetchedRef.current < STALE_MS) return
    let cancelled = false
    Promise.all([api.listTasks({ limit: TASKS_LIMIT }), api.listAllRuns({ limit: RUNS_LIMIT })])
      .then(([tasksRes, runsRes]) => {
        if (cancelled) return
        setTasks(tasksRes.tasks)
        setRuns(runsRes)
        lastFetchedRef.current = Date.now()
      })
      .catch(() => {
        // background refresh — leave existing data in place
      })
    return () => {
      cancelled = true
    }
  }, [activeTab, loading, api])

  const total = tasks.length
  const activeTasks = tasks.filter((t) => !['archived', 'done'].includes(t.status)).length
  const doneTasks = tasks.filter((t) => t.status === 'done').length

  const summaryCards = [
    { label: 'Total', value: total },
    { label: 'Active', value: activeTasks, accentColor: STATUS_COLOR_VAR.doing },
    { label: 'Done', value: doneTasks, accentColor: STATUS_COLOR_VAR.done },
  ]

  function handleTabChange(next: string) {
    if (!isDashboardTab(next)) return
    setSearchParams(
      (prev) => {
        const params = new URLSearchParams(prev)
        if (next === 'activity') params.delete('tab')
        else params.set('tab', next)
        return params
      },
      { replace: true }
    )
  }

  if (error) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="Ops Dashboard">
          <SseStatusPill status={sseStatus} />
          <RestartFrontendButton />
        </PageHeader>
        <EmptyState
          variant="error"
          title="Something went wrong"
          description={error}
          action={{ label: 'Retry', onClick: () => window.location.reload() }}
        />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Ops Dashboard">
        <SseStatusPill status={sseStatus} />
        <RestartFrontendButton />
      </PageHeader>
      {!loading && <SummaryCards cards={summaryCards} />}
      <div className="flex-1 overflow-auto">
        {loading ? (
          <DashboardSkeleton />
        ) : (
          <Tabs value={activeTab} onValueChange={handleTabChange} className="gap-0">
            <TabsList className="mx-4 mt-3">
              <TabsTrigger value="activity">Activity</TabsTrigger>
              <TabsTrigger value="mission-control">Mission Control</TabsTrigger>
              <TabsTrigger value="usage">Usage</TabsTrigger>
            </TabsList>

            <TabsContent value="activity" keepMounted className="px-4 py-4">
              <ActivityTabLayout tasks={tasks} runs={runs} events={events} />
            </TabsContent>
            <TabsContent value="mission-control" keepMounted className="px-4 py-4">
              <MissionControlTabLayout tasks={tasks} runs={runs} />
            </TabsContent>
            <TabsContent value="usage" keepMounted className="px-4 py-4">
              <UsageTabLayout runs={runs} />
            </TabsContent>
          </Tabs>
        )}
      </div>
    </div>
  )
}

function SseStatusPill({ status }: { status: SseStatus }) {
  const label = status === 'live' ? 'live' : status
  const dotClass =
    status === 'live'
      ? 'animate-pulse bg-emerald-400'
      : status === 'error'
        ? 'bg-red-500'
        : 'bg-zinc-600'
  const textClass =
    status === 'live'
      ? 'text-emerald-400'
      : status === 'error'
        ? 'text-red-400'
        : 'text-zinc-500'
  return (
    <span
      className={cn(
        'flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.18em]',
        textClass
      )}
      aria-live="polite"
    >
      <span className={cn('h-1.5 w-1.5 rounded-full', dotClass)} />
      {label}
    </span>
  )
}

function ActivityTabLayout({
  tasks,
  runs,
  events,
}: {
  tasks: Task[]
  runs: Run[]
  events: SSEEvent[]
}) {
  // Pulse derives from the live event buffer: each run.started event is a
  // data point in the 24h bar strip. We synthesize Run-shaped rows so the
  // existing Pulse24h widget keeps its started_at contract without a rewrite.
  const pulseRuns = useMemo<Run[]>(() => {
    const rows: Run[] = []
    for (const ev of events) {
      if (ev.type !== 'run.started') continue
      const row = synthRunFromStartEvent(ev)
      if (row) rows.push(row)
    }
    return rows
  }, [events])

  return (
    <div className="flex flex-col gap-4">
      <Card size="sm">
        <CardContent>
          <div className="mb-3 flex items-center justify-between gap-4">
            <span className="whitespace-nowrap font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground">
              Activity — 16w
            </span>
            <span className="font-mono text-[9px] text-muted-foreground/70">tasks · runs</span>
          </div>
          <ActivityHeatmap tasks={tasks} runs={runs} weekCount={16} />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[1fr_minmax(320px,420px)]">
        <Card size="sm">
          <CardContent>
            <Pulse24h runs={pulseRuns} title="24h pulse — live" />
          </CardContent>
        </Card>
        <Card size="sm">
          <CardContent>
            <RecentRuns runs={runs} limit={12} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function MissionControlTabLayout({ tasks, runs }: { tasks: Task[]; runs: Run[] }) {
  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-[1fr_minmax(260px,340px)]">
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_minmax(240px,320px)]">
          <Card size="sm">
            <CardContent>
              <RunsChart runs={runs} days={14} />
            </CardContent>
          </Card>
          <Card size="sm">
            <CardContent>
              <RunStatusDistribution runs={runs} />
            </CardContent>
          </Card>
        </div>
        <Card size="sm">
          <CardContent>
            <TokenThroughput runs={runs} days={14} />
          </CardContent>
        </Card>
      </div>
      <Card size="sm">
        <CardContent>
          <TaskPipeline tasks={tasks} />
        </CardContent>
      </Card>
    </div>
  )
}

function UsageTabLayout({ runs }: { runs: Run[] }) {
  return (
    <div className="flex flex-col gap-4">
      <Card size="sm">
        <CardContent>
          <CostPerDay runs={runs} days={14} />
        </CardContent>
      </Card>
      <Card size="sm">
        <CardContent>
          <div className="flex min-h-[120px] items-center justify-center font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground/60">
            Per-provider / per-model breakdowns — coming soon
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
