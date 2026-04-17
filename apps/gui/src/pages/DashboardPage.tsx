import { useState, useEffect, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { EmptyState } from '@/components/domain/empty-state'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { OpsWidgetGroupA, OpsWidgetGroupB, OpsWidgetGroupC } from '@/components/widgets'
import { useApi } from '@/hooks/use-api'
import { STATUS_COLOR_VAR } from '@/lib/constants'
import type { Run, Task } from '@/lib/types'

type DashboardTab = 'activity' | 'mission-control' | 'usage'
const TAB_VALUES: DashboardTab[] = ['activity', 'mission-control', 'usage']

function isDashboardTab(v: string | null): v is DashboardTab {
  return !!v && (TAB_VALUES as string[]).includes(v)
}

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

export default function DashboardPage() {
  const api = useApi()
  const [searchParams, setSearchParams] = useSearchParams()

  const [tasks, setTasks] = useState<Task[]>([])
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const activeTab: DashboardTab = useMemo(() => {
    const raw = searchParams.get('tab')
    return isDashboardTab(raw) ? raw : 'activity'
  }, [searchParams])

  useEffect(() => {
    let cancelled = false
    api
      .listTasks({ limit: 500 })
      .then(({ tasks: t }) => {
        if (cancelled) return t
        setTasks(t)
        return t
      })
      .then(async (t) => {
        if (cancelled || !t) return
        // Hydrate recent runs so the RunsChart + ActivityHeatmap have some
        // data today. A cross-task runs endpoint (CW-20260417-0091) will
        // later replace this fan-out.
        const recent = t
          .slice()
          .sort(
            (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
          )
          .slice(0, 20)
        const lists = await Promise.all(
          recent.map((x) => api.listRuns(x.id).catch(() => [] as Run[]))
        )
        if (!cancelled) setRuns(lists.flat())
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
          <RestartFrontendButton />
        </PageHeader>
        <EmptyState
          variant="error"
          description={error}
          action={{ label: 'Retry', onClick: () => window.location.reload() }}
        />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Ops Dashboard">
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

            <TabsContent value="activity" className="px-4 py-4">
              <OpsWidgetGroupA tasks={tasks} runs={runs} />
            </TabsContent>
            <TabsContent value="mission-control" className="px-4 py-4">
              <OpsWidgetGroupB runs={runs} />
            </TabsContent>
            <TabsContent value="usage" className="px-4 py-4">
              <OpsWidgetGroupC runs={runs} />
            </TabsContent>
          </Tabs>
        )}
      </div>
    </div>
  )
}
