import { useState, useEffect, useCallback } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS } from '@/lib/constants'
import type { Task, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const SSE_EVENTS = ['task.created', 'task.updated', 'task.transitioned']

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full rounded-md" />
      ))}
    </div>
  )
}

export default function BoardPage() {
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeStatuses, setActiveStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  const [mode, setMode] = useState<ModePreset>('all')
  const [activePriorities, setActivePriorities] = useState<number[]>([])

  const fetchTasks = useCallback(async () => {
    try {
      const result = await api.listTasks({
        status: activeStatuses,
      })
      setTasks(result.tasks)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load tasks')
    } finally {
      setLoading(false)
    }
  }, [api, activeStatuses])

  useEffect(() => {
    setLoading(true)
    fetchTasks()
  }, [fetchTasks])

  // Refresh on SSE events
  useEffect(() => {
    if (lastEvent) fetchTasks()
  }, [lastEvent, fetchTasks])

  function handleStatusToggle(status: TaskStatus) {
    setActiveStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
    )
  }

  function handlePriorityToggle(priority: number) {
    setActivePriorities((prev) =>
      prev.includes(priority) ? prev.filter((p) => p !== priority) : [...prev, priority]
    )
  }

  function handleModeChange(newMode: ModePreset) {
    setMode(newMode)
    if (newMode === 'all') {
      setActiveStatuses(DEFAULT_ACTIVE_STATUSES)
    } else {
      setActiveStatuses(MODE_PRESETS[newMode])
    }
  }

  async function handleTransition(id: string, status: TaskStatus) {
    try {
      await api.transitionTask(id, status)
      fetchTasks()
    } catch {
      // silently fail — user will see no change
    }
  }

  const emptyVariant = activeStatuses.length < DEFAULT_ACTIVE_STATUSES.length
    ? 'no-results'
    : 'no-tasks'

  const openCount = tasks.filter((t) => ['backlog', 'todo', 'queued'].includes(t.status)).length
  const doingCount = tasks.filter((t) => t.status === 'doing').length
  const reviewCount = tasks.filter((t) => t.status === 'review').length
  const blockedCount = tasks.filter((t) => t.status === 'blocked').length

  const summaryCards = [
    { label: 'Open Tasks', value: openCount },
    { label: 'In Progress', value: doingCount, accentColor: '#60a5fa' },
    { label: 'In Review', value: reviewCount, accentColor: '#a78bfa' },
    { label: 'Blocked', value: blockedCount, accentColor: '#f87171' },
  ]

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Tasks" />
      {!loading && !error && <SummaryCards cards={summaryCards} />}
      <FilterBar
        activeStatuses={activeStatuses}
        onStatusToggle={handleStatusToggle}
        mode={mode}
        onModeChange={handleModeChange}
        activePriorities={activePriorities}
        onPriorityToggle={handlePriorityToggle}
      />
      <div className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : error ? (
          <EmptyState
            variant="error"
            description={error}
            action={{ label: 'Retry', onClick: fetchTasks }}
          />
        ) : (
          <TaskTable
            tasks={tasks}
            onTransition={handleTransition}
            emptyVariant={emptyVariant}
          />
        )}
      </div>
    </div>
  )
}
