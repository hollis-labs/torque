import { useState, useEffect, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { DetailHeader } from '@/components/domain/detail-header'
import { PriorityBadge } from '@/components/domain/priority-badge'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS } from '@/lib/constants'
import type { Epic, Task, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const SSE_EVENTS = ['epic.updated', 'task.created', 'task.updated', 'task.transitioned']

export default function EpicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [epic, setEpic] = useState<Epic | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Task state
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [activeStatuses, setActiveStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  const [mode, setMode] = useState<ModePreset>('all')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    api.getEpic(id)
      .then((e) => { setEpic(e); setError(null) })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  const fetchTasks = useCallback(async () => {
    if (!id) return
    try {
      const result = await api.listTasks({ epic_id: id, status: activeStatuses })
      setTasks(result.tasks)
    } catch {
      setTasks([])
    }
  }, [api, id, activeStatuses])

  // Fetch tasks when epic is loaded and when filter changes
  useEffect(() => {
    if (epic) fetchTasks()
  }, [epic, fetchTasks])

  // SSE refresh
  useEffect(() => {
    if (!lastEvent || !id) return
    api.getEpic(id).then(setEpic).catch(() => {})
    if (tasks !== null) fetchTasks()
  }, [lastEvent]) // eslint-disable-line react-hooks/exhaustive-deps

  function handleStatusToggle(status: TaskStatus) {
    setActiveStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
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

  async function handleTransition(taskId: string, status: TaskStatus) {
    try {
      await api.transitionTask(taskId, status)
      fetchTasks()
    } catch {
      // no-op
    }
  }

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !epic) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Epic not found.'} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <DetailHeader
        title={epic.name}
        backTo="/epics"
        backLabel="Epics"
        id={epic.id}
        status={epic.status}
      >
        {epic.priority !== null && epic.priority !== undefined && (
          <PriorityBadge priority={epic.priority} />
        )}
      </DetailHeader>

      {/* Description */}
      {epic.description && (
        <div className="px-6 py-3 border-b border-zinc-800/80">
          <span className="text-xs text-zinc-500 block mb-1">Description</span>
          <p className="text-sm text-zinc-300 whitespace-pre-wrap">{epic.description}</p>
        </div>
      )}

      {/* Tasks */}
      <div className="flex-1 overflow-auto">
        <FilterBar
          activeStatuses={activeStatuses}
          onStatusToggle={handleStatusToggle}
          mode={mode}
          onModeChange={handleModeChange}
        />
        <div>
          {tasks === null ? (
            <div className="flex flex-col gap-2 p-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full rounded-md" />
              ))}
            </div>
          ) : (
            <TaskTable
              tasks={tasks}
              onTransition={handleTransition}
              emptyVariant="no-results"
            />
          )}
        </div>
      </div>
    </div>
  )
}
