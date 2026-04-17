import { useState, useEffect, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { EmptyState } from '@/components/domain/empty-state'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { useApi } from '@/hooks/use-api'
import { STATUS_COLOR_VAR } from '@/lib/constants'
import type { Task } from '@/lib/types'

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

function TabPlaceholder({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
      {label} — coming soon
    </div>
  )
}

export default function DashboardPage() {
  const api = useApi()
  const [searchParams, setSearchParams] = useSearchParams()

  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const activeTab: DashboardTab = useMemo(() => {
    const raw = searchParams.get('tab')
    return isDashboardTab(raw) ? raw : 'activity'
  }, [searchParams])

  useEffect(() => {
    let cancelled = false
    api
      .listTasks({ limit: 200 })
      .then(({ tasks: t }) => {
        if (!cancelled) setTasks(t)
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
              <TabPlaceholder label="Activity" />
            </TabsContent>
            <TabsContent value="mission-control" className="px-4 py-4">
              <TabPlaceholder label="Mission Control" />
            </TabsContent>
            <TabsContent value="usage" className="px-4 py-4">
              <TabPlaceholder label="Usage" />
            </TabsContent>
          </Tabs>
        )}
      </div>
    </div>
  )
}
