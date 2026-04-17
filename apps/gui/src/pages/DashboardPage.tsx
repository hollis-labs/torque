import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/domain/status-badge'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { TASK_STATUSES, STATUS_COLOR_VAR } from '@/lib/constants'
import { hasBlockedReason, truncateBlockedReason } from '@/lib/blocked-reason'
import { formatRelativeTime } from '@/lib/utils'
import type { Task, TaskStatus } from '@/lib/types'

interface StatusCount {
  status: TaskStatus
  count: number
}

export default function DashboardPage() {
  const api = useApi()
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.listTasks({ limit: 200 }).then(({ tasks: t }) => {
      setTasks(t)
    }).catch((err: Error) => {
      setError(err.message)
    }).finally(() => {
      setLoading(false)
    })
  }, [api])

  const statusCounts: StatusCount[] = TASK_STATUSES.map((status) => ({
    status,
    count: tasks.filter((t) => t.status === status).length,
  }))

  const total = tasks.length
  const activeTasks = tasks.filter((t) => !['archived', 'done'].includes(t.status)).length
  const doneTasks = tasks.filter((t) => t.status === 'done').length

  const recentTasks = [...tasks]
    .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    .slice(0, 5)

  const maxCount = Math.max(...statusCounts.map((s) => s.count), 1)

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-40" />
        <div className="grid grid-cols-3 gap-4">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-24 rounded-lg" />)}
        </div>
        <Skeleton className="h-48 rounded-lg" />
        <Skeleton className="h-64 rounded-lg" />
      </div>
    )
  }

  if (error) {
    return (
      <EmptyState
        variant="error"
        description={error}
        action={{ label: 'Retry', onClick: () => window.location.reload() }}
      />
    )
  }

  return (
    <div className="flex h-full flex-col overflow-auto">
      <div className="border-b border-border bg-card px-6 py-4">
        <h1 className="text-lg font-semibold text-foreground">Dashboard</h1>
      </div>

      <div className="p-6 flex flex-col gap-6 max-w-3xl">
        {/* Summary cards */}
        <div className="grid grid-cols-3 gap-4">
          <Card>
            <CardHeader className="pb-1">
              <CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Total Tasks</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-3xl font-bold text-foreground">{total}</p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="pb-1">
              <CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Active</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-3xl font-bold" style={{ color: 'var(--color-status-doing)' }}>{activeTasks}</p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="pb-1">
              <CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Done</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-3xl font-bold" style={{ color: 'var(--color-status-done)' }}>{doneTasks}</p>
            </CardContent>
          </Card>
        </div>

        {/* Status distribution */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-semibold">Status Distribution</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex flex-col gap-2.5">
              {statusCounts.filter((s) => s.count > 0).map(({ status, count }) => (
                <div key={status} className="flex items-center gap-3">
                  <div className="w-24 shrink-0">
                    <StatusBadge status={status} />
                  </div>
                  <div className="flex-1 h-2 rounded-full bg-muted overflow-hidden">
                    <div
                      className="h-full rounded-full transition-all"
                      style={{
                        width: `${(count / maxCount) * 100}%`,
                        backgroundColor: STATUS_COLOR_VAR[status],
                      }}
                    />
                  </div>
                  <span className="text-sm font-mono text-muted-foreground w-6 text-right">{count}</span>
                </div>
              ))}
              {statusCounts.every((s) => s.count === 0) && (
                <p className="text-sm text-muted-foreground text-center py-4">No tasks yet.</p>
              )}
            </div>
          </CardContent>
        </Card>

        {/* Recent tasks */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-semibold">Recent Activity</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {recentTasks.length === 0 ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">No recent tasks.</p>
            ) : (
              <ul className="divide-y divide-border">
                {recentTasks.map((task) => (
                  <li key={task.id} className="flex items-center justify-between gap-3 px-6 py-3">
                    <div className="flex items-center gap-2 min-w-0">
                      <StatusBadge
                        status={task.status}
                        tooltip={
                          hasBlockedReason(task)
                            ? truncateBlockedReason(task.blocked_reason)
                            : undefined
                        }
                      />
                      <Link
                        to={`/tasks/${task.id}`}
                        className="text-sm font-medium text-foreground hover:text-primary hover:underline underline-offset-4 truncate"
                      >
                        {task.title}
                      </Link>
                    </div>
                    <span className="text-xs text-muted-foreground whitespace-nowrap shrink-0">
                      {formatRelativeTime(task.updated_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
