import { useState, useEffect, useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { EmptyState } from '@/components/domain/empty-state'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS, TASK_STATUSES } from '@/lib/constants'
import type { Epic, Project, Sprint, Tag, Task, TaskStatus } from '@/lib/types'

type ModePreset = keyof typeof MODE_PRESETS | 'all'

const SSE_EVENTS = ['task.created', 'task.updated', 'task.transitioned']
const MODE_VALUES: ModePreset[] = ['all', 'planning', 'executing', 'reviewing']

function parseStatusParam(raw: string | null): TaskStatus[] {
  if (raw === null) return DEFAULT_ACTIVE_STATUSES
  const allowed = new Set<string>(TASK_STATUSES)
  const parsed = raw
    .split(',')
    .map((s) => s.trim())
    .filter((s) => allowed.has(s)) as TaskStatus[]
  return parsed
}

function parsePriorityParam(raw: string | null): number[] {
  if (!raw) return []
  return raw
    .split(',')
    .map((s) => Number.parseInt(s.trim(), 10))
    .filter((n) => Number.isInteger(n) && n >= 1 && n <= 3)
}

function parseModeParam(raw: string | null): ModePreset {
  if (raw && MODE_VALUES.includes(raw as ModePreset)) return raw as ModePreset
  return 'all'
}

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
  const [searchParams, setSearchParams] = useSearchParams()

  // Filter state derived from URL (URL is source of truth for round-tripping)
  const activeStatuses = useMemo(
    () => parseStatusParam(searchParams.get('status')),
    [searchParams]
  )
  const activePriorities = useMemo(
    () => parsePriorityParam(searchParams.get('priority')),
    [searchParams]
  )
  const mode = useMemo(
    () => parseModeParam(searchParams.get('mode')),
    [searchParams]
  )
  const projectId = searchParams.get('project_id')
  const sprintId = searchParams.get('sprint_id')
  const epicId = searchParams.get('epic_id')
  const tagSlug = searchParams.get('tag')

  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Group picker data
  const [projects, setProjects] = useState<Project[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [tags, setTags] = useState<Tag[]>([])

  // Create-modal state
  const [projectCreateOpen, setProjectCreateOpen] = useState(false)
  const [epicCreateOpen, setEpicCreateOpen] = useState(false)

  // Fetch pickers once on mount
  const refreshPickers = useCallback(async () => {
    const [p, s, e, t] = await Promise.all([
      api.listProjects().catch(() => ({ projects: [] as Project[] })),
      api.listSprints().catch(() => ({ sprints: [] as Sprint[] })),
      api.listEpics().catch(() => ({ epics: [] as Epic[] })),
      api.listTags().catch(() => ({ tags: [] as Tag[] })),
    ])
    setProjects(p.projects)
    setSprints(s.sprints)
    setEpics(e.epics)
    setTags(t.tags)
  }, [api])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      if (!cancelled) await refreshPickers()
    })()
    return () => {
      cancelled = true
    }
  }, [refreshPickers])

  // Cascade sprint/epic options by selected project (client-side filter)
  const visibleSprints = useMemo(
    () => (projectId ? sprints.filter((s) => s.project_id === projectId) : sprints),
    [sprints, projectId]
  )
  const visibleEpics = useMemo(
    () => (projectId ? epics.filter((e) => e.project_id === projectId) : epics),
    [epics, projectId]
  )

  // Auto-clear sprint/epic selection if it falls outside the cascaded set.
  useEffect(() => {
    const sprintInvalid = sprintId && !visibleSprints.some((s) => s.id === sprintId)
    const epicInvalid = epicId && !visibleEpics.some((e) => e.id === epicId)
    if (!sprintInvalid && !epicInvalid) return
    // Wait until we've actually loaded the lists; otherwise everything looks "invalid".
    if (sprints.length === 0 && epics.length === 0) return
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (sprintInvalid) next.delete('sprint_id')
        if (epicInvalid) next.delete('epic_id')
        return next
      },
      { replace: true }
    )
  }, [projectId, sprintId, epicId, visibleSprints, visibleEpics, sprints.length, epics.length, setSearchParams])

  const fetchTasks = useCallback(async () => {
    try {
      const result = await api.listTasks({
        status: activeStatuses,
        priority: activePriorities.length ? activePriorities : undefined,
        project_id: projectId ?? undefined,
        sprint_id: sprintId ?? undefined,
        epic_id: epicId ?? undefined,
        tags: tagSlug ? [tagSlug] : undefined,
      })
      setTasks(result.tasks)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load tasks')
    } finally {
      setLoading(false)
    }
  }, [api, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug])

  useEffect(() => {
    setLoading(true)
    fetchTasks()
  }, [fetchTasks])

  // Refresh on SSE events
  useEffect(() => {
    if (lastEvent) fetchTasks()
  }, [lastEvent, fetchTasks])

  function updateParams(mutate: (params: URLSearchParams) => void) {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        mutate(next)
        return next
      },
      { replace: true }
    )
  }

  function setStatusList(next: TaskStatus[]) {
    updateParams((p) => {
      const sameAsDefault =
        next.length === DEFAULT_ACTIVE_STATUSES.length &&
        DEFAULT_ACTIVE_STATUSES.every((s) => next.includes(s))
      if (sameAsDefault) p.delete('status')
      else p.set('status', next.join(','))
    })
  }

  function handleStatusToggle(status: TaskStatus) {
    const next = activeStatuses.includes(status)
      ? activeStatuses.filter((s) => s !== status)
      : [...activeStatuses, status]
    setStatusList(next)
  }

  function handlePriorityToggle(priority: number) {
    const next = activePriorities.includes(priority)
      ? activePriorities.filter((p) => p !== priority)
      : [...activePriorities, priority]
    updateParams((p) => {
      if (next.length === 0) p.delete('priority')
      else p.set('priority', next.join(','))
    })
  }

  function handleModeChange(newMode: ModePreset) {
    updateParams((p) => {
      if (newMode === 'all') {
        p.delete('mode')
        p.delete('status')
      } else {
        p.set('mode', newMode)
        p.set('status', MODE_PRESETS[newMode].join(','))
      }
    })
  }

  function handleGroupChange(key: 'project_id' | 'sprint_id' | 'epic_id' | 'tag', value: string | null) {
    updateParams((p) => {
      if (value === null) p.delete(key)
      else p.set(key, value)
    })
  }

  async function handleTransition(id: string, status: TaskStatus) {
    try {
      await api.transitionTask(id, status)
      fetchTasks()
    } catch (err) {
      notifyError(err, 'Failed to update task status')
    }
  }

  const filtersActive =
    activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ||
    activePriorities.length > 0 ||
    projectId !== null ||
    sprintId !== null ||
    epicId !== null ||
    tagSlug !== null

  const emptyVariant = filtersActive ? 'no-results' : 'no-tasks'

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
        projects={projects}
        projectId={projectId}
        onProjectChange={(id) => handleGroupChange('project_id', id)}
        onProjectCreate={() => setProjectCreateOpen(true)}
        sprints={visibleSprints}
        sprintId={sprintId}
        onSprintChange={(id) => handleGroupChange('sprint_id', id)}
        epics={visibleEpics}
        epicId={epicId}
        onEpicChange={(id) => handleGroupChange('epic_id', id)}
        onEpicCreate={() => setEpicCreateOpen(true)}
        tags={tags}
        tagSlug={tagSlug}
        onTagChange={(slug) => handleGroupChange('tag', slug)}
      />

      <ProjectCreateDialog
        open={projectCreateOpen}
        onOpenChange={setProjectCreateOpen}
        onCreated={(p) => {
          refreshPickers()
          handleGroupChange('project_id', p.id)
        }}
      />
      <EpicCreateDialog
        open={epicCreateOpen}
        onOpenChange={setEpicCreateOpen}
        projects={projects}
        defaultProjectId={projectId}
        onCreated={(e) => {
          refreshPickers()
          handleGroupChange('epic_id', e.id)
        }}
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
            onTaskChange={(updated) =>
              setTasks((prev) => prev.map((t) => (t.id === updated.id ? updated : t)))
            }
            onTaskDelete={(id) => setTasks((prev) => prev.filter((t) => t.id !== id))}
            emptyVariant={emptyVariant}
          />
        )}
      </div>
    </div>
  )
}
