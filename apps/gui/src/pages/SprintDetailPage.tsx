import { useState, useEffect, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { DetailHeader } from '@/components/domain/detail-header'
import { ProgressBar } from '@/components/domain/progress-bar'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS } from '@/lib/constants'
import type { Sprint, Task, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const SSE_EVENTS = ['sprint.updated', 'task.created', 'task.updated', 'task.transitioned']

export default function SprintDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [sprint, setSprint] = useState<Sprint | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [projectName, setProjectName] = useState<string | null>(null)

  // Task state
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [activeStatuses, setActiveStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  const [mode, setMode] = useState<ModePreset>('all')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    api.getSprint(id)
      .then((s) => {
        setSprint(s)
        setError(null)
        // Fetch project name if linked
        if (s.project_id) {
          api.getProject(s.project_id)
            .then((p) => setProjectName(p.name))
            .catch(() => setProjectName(null))
        }
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  const fetchTasks = useCallback(async () => {
    if (!id) return
    try {
      const result = await api.listTasks({ sprint_id: id, status: activeStatuses })
      setTasks(result.tasks)
    } catch {
      setTasks([])
    }
  }, [api, id, activeStatuses])

  // Fetch tasks on mount and when filter changes
  useEffect(() => {
    if (sprint) fetchTasks()
  }, [sprint, fetchTasks])

  // SSE refresh
  useEffect(() => {
    if (!lastEvent || !id) return
    api.getSprint(id).then(setSprint).catch(() => {})
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
    } catch (err) {
      notifyError(err, 'Failed to update task status')
    }
  }

  if (loading) {
    return (
      <div className="p-6 flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-5 w-96" />
        <Skeleton className="h-1 w-full rounded-full" />
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    )
  }

  if (error || !sprint) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Sprint not found.'} />
      </div>
    )
  }

  const allTasks = tasks ?? []
  const doneTasks = allTasks.filter((t) => t.status === 'done').length
  const totalTasks = allTasks.length
  const progress = totalTasks > 0 ? Math.round((doneTasks / totalTasks) * 100) : 0

  return (
    <div className="flex h-full flex-col">
      <DetailHeader
        title={sprint.name}
        backTo="/operations"
        backLabel="Operations"
        id={sprint.id}
        status={sprint.status}
      />

      {/* Metadata */}
      <div className="px-6 py-3 border-b border-zinc-800/80 flex flex-wrap gap-6 text-sm">
        <div>
          <span className="text-xs text-zinc-500 block mb-0.5">Approval Mode</span>
          <span className="text-xs text-zinc-300">{sprint.approval_mode || 'auto'}</span>
        </div>
        {sprint.cost_budget !== null && sprint.cost_budget !== undefined && (
          <div>
            <span className="text-xs text-zinc-500 block mb-0.5">Cost Budget</span>
            <span className="text-xs text-zinc-300">${sprint.cost_budget.toFixed(2)}</span>
          </div>
        )}
        {projectName && (
          <div>
            <span className="text-xs text-zinc-500 block mb-0.5">Project</span>
            <span className="text-xs text-zinc-300">{projectName}</span>
          </div>
        )}
      </div>

      {/* Progress bar */}
      <div className="px-6 py-3 border-b border-zinc-800/80">
        <div className="flex items-center justify-between text-xs text-zinc-400 mb-1.5">
          <span>Progress</span>
          <span>{doneTasks} / {totalTasks} tasks done ({progress}%)</span>
        </div>
        <ProgressBar value={progress} />
      </div>

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
              onTaskChange={(updated) =>
                setTasks((prev) => prev?.map((t) => (t.id === updated.id ? updated : t)) ?? prev)
              }
              onTaskDelete={(deletedId) =>
                setTasks((prev) => prev?.filter((t) => t.id !== deletedId) ?? prev)
              }
              emptyVariant="no-results"
            />
          )}
        </div>
      </div>
    </div>
  )
}
