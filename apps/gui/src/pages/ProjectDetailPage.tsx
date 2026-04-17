import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Skeleton } from '@/components/ui/skeleton'
import { DetailHeader } from '@/components/domain/detail-header'
import { ProgressBar } from '@/components/domain/progress-bar'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { StatusBadge } from '@/components/domain/status-badge'
import { CopyableId } from '@/components/domain/copyable-id'
import { EmptyState } from '@/components/domain/empty-state'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS } from '@/lib/constants'
import type { Project, Sprint, Task, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const SSE_EVENTS = ['project.updated', 'task.created', 'task.updated', 'task.transitioned', 'sprint.created', 'sprint.updated']

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const api = useApi()
  const navigate = useNavigate()
  const { lastEvent } = useSSE(SSE_EVENTS)

  const [project, setProject] = useState<Project | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Tab state
  const [activeTab, setActiveTab] = useState('sprints')
  const [sprints, setSprints] = useState<Sprint[] | null>(null)
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [activeStatuses, setActiveStatuses] = useState<TaskStatus[]>(DEFAULT_ACTIVE_STATUSES)
  const [mode, setMode] = useState<ModePreset>('all')

  useEffect(() => {
    if (!id) return
    setLoading(true)
    api.getProject(id)
      .then((p) => { setProject(p); setError(null) })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false))
  }, [api, id])

  const fetchTasks = useCallback(async () => {
    if (!id) return
    try {
      const result = await api.listTasks({ project_id: id, status: activeStatuses })
      setTasks(result.tasks)
    } catch {
      setTasks([])
    }
  }, [api, id, activeStatuses])

  // Lazy load tab data
  useEffect(() => {
    if (!id || !project) return
    if (activeTab === 'sprints' && sprints === null) {
      api.listSprints({ project_id: id })
        .then((r) => setSprints(r.sprints))
        .catch(() => setSprints([]))
    }
    if (activeTab === 'tasks' && tasks === null) {
      fetchTasks()
    }
  }, [activeTab, id, project, sprints, tasks, api, fetchTasks])

  // Refetch tasks when status filter changes
  useEffect(() => {
    if (activeTab === 'tasks' && tasks !== null) {
      fetchTasks()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeStatuses])

  // SSE refresh
  useEffect(() => {
    if (!lastEvent || !id) return
    api.getProject(id).then(setProject).catch(() => {})
    if (sprints !== null) {
      api.listSprints({ project_id: id }).then((r) => setSprints(r.sprints)).catch(() => {})
    }
    if (tasks !== null) {
      fetchTasks()
    }
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

  if (error || !project) {
    return (
      <div className="p-6">
        <EmptyState variant="error" description={error ?? 'Project not found.'} />
      </div>
    )
  }

  // Calculate progress from all tasks (need to fetch all statuses for this)
  const allTasks = tasks ?? []
  const doneTasks = allTasks.filter((t) => t.status === 'done').length
  const totalTasks = allTasks.length
  const progress = totalTasks > 0 ? Math.round((doneTasks / totalTasks) * 100) : 0

  return (
    <div className="flex h-full flex-col">
      <DetailHeader
        title={project.name}
        backTo="/operations"
        backLabel="Operations"
        id={project.id}
        status={project.status}
      />

      {/* Progress bar */}
      <div className="px-6 py-3 border-b border-zinc-800/80">
        <div className="flex items-center justify-between text-xs text-zinc-400 mb-1.5">
          <span>Progress</span>
          <span>{doneTasks} / {totalTasks} tasks done ({progress}%)</span>
        </div>
        <ProgressBar value={progress} />
      </div>

      {/* Metadata */}
      <div className="px-6 py-3 border-b border-zinc-800/80 flex flex-wrap gap-6 text-sm">
        {project.repo_path && (
          <div>
            <span className="text-xs text-zinc-500 block mb-0.5">Repo</span>
            <span className="font-mono text-xs text-zinc-300">{project.repo_path}</span>
          </div>
        )}
        {project.description && (
          <div>
            <span className="text-xs text-zinc-500 block mb-0.5">Description</span>
            <span className="text-xs text-zinc-300">{project.description}</span>
          </div>
        )}
      </div>

      {/* Tabs */}
      <div className="flex-1 overflow-auto p-6">
        <Tabs value={activeTab} onValueChange={setActiveTab}>
          <TabsList className="mb-4">
            <TabsTrigger value="sprints">Sprints</TabsTrigger>
            <TabsTrigger value="tasks">Tasks</TabsTrigger>
          </TabsList>

          <TabsContent value="sprints">
            {sprints === null ? (
              <div className="flex flex-col gap-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <Skeleton key={i} className="h-10 w-full rounded-md" />
                ))}
              </div>
            ) : sprints.length === 0 ? (
              <EmptyState variant="no-results" title="No sprints" description="No sprints linked to this project." />
            ) : (
              <div className="overflow-x-auto">
                <table className="min-w-full">
                  <thead className="text-[10px] uppercase tracking-[.28em] text-zinc-500">
                    <tr className="border-b border-zinc-800/80">
                      <th className="px-3 py-2 text-left font-medium">Sprint</th>
                      <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">Status</th>
                      <th className="w-px whitespace-nowrap px-2 py-2 text-right font-medium">Updated</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-zinc-800/60 text-[13px] leading-4">
                    {sprints.map((sprint) => (
                      <tr
                        key={sprint.id}
                        className="cursor-pointer hover:bg-zinc-900/50 transition-colors"
                        onClick={() => navigate(`/sprints/${sprint.id}`)}
                      >
                        <td className="px-3 py-2.5">
                          <div className="flex flex-col gap-0.5">
                            <span className="font-medium text-zinc-100">{sprint.name}</span>
                            <CopyableId id={sprint.id} />
                          </div>
                        </td>
                        <td className="px-2 py-2.5 text-right">
                          <StatusBadge status={sprint.status} />
                        </td>
                        <td className="px-2 py-2.5 text-right whitespace-nowrap text-zinc-400">
                          {new Date(sprint.updated_at).toLocaleDateString()}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </TabsContent>

          <TabsContent value="tasks">
            <FilterBar
              activeStatuses={activeStatuses}
              onStatusToggle={handleStatusToggle}
              mode={mode}
              onModeChange={handleModeChange}
            />
            <div className="mt-2">
              {tasks === null ? (
                <div className="flex flex-col gap-2">
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
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
