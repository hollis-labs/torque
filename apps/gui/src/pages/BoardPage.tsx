import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useSearchParams, useLocation } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { EmptyState } from '@/components/domain/empty-state'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, MODE_PRESETS, TASK_STATUSES } from '@/lib/constants'
import { saveTaskListCursor } from '@/lib/task-list-cursor'
import { saveOpsFilters, readOpsFilters, clearOpsFilters } from '@/lib/ops-filters-storage'
import type { Epic, Project, Sprint, Tag, Task, TaskStatus } from '@/lib/types'

const FILTER_PARAM_KEYS = ['status', 'priority', 'project_id', 'sprint_id', 'epic_id', 'tag', 'mode'] as const

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
  const location = useLocation()

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

  // Hydrate filter state from localStorage each time we arrive at this
  // route. URL params win: if any filter-relevant param is present, we
  // skip storage entirely so deep links keep working as authored.
  //
  // Why this keys on location.key rather than running once per mount: the
  // previous one-shot-flag approach only rehydrated on a fresh BoardPage
  // mount, which broke in-app navigation back to /operations (task
  // detail → back arrow / Operations link). Under React 19 + Router v7
  // the component can't be relied on to unmount cleanly between navs in
  // every scenario, so a per-mount flag stayed `true` and the effect was
  // skipped on the return trip — leaving storage ignored and the table
  // unfiltered until a hard reload reset the flag.
  //
  // `location.key` is unique per history entry, so each distinct
  // navigation arrival gets exactly one restoration attempt, whether the
  // component remounts or stays put. The write we do via
  // `setSearchParams({ replace: true })` keeps the same key, so we don't
  // loop on our own URL edit. The `hydrated` state still gates downstream
  // effects (save, fetch) so they skip the very first pre-hydrate render
  // on a fresh mount.
  const [hydrated, setHydrated] = useState(false)
  const lastHydratedKey = useRef<string | null>(null)
  useEffect(() => {
    if (lastHydratedKey.current === location.key) return
    lastHydratedKey.current = location.key

    const urlHasFilter = FILTER_PARAM_KEYS.some((k) => searchParams.has(k))
    if (urlHasFilter) {
      setHydrated(true)
      return
    }
    const stored = readOpsFilters()
    if (!stored) {
      setHydrated(true)
      return
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        const sameAsDefault =
          stored.statuses.length === DEFAULT_ACTIVE_STATUSES.length &&
          DEFAULT_ACTIVE_STATUSES.every((s) => stored.statuses.includes(s))
        if (stored.statuses.length > 0 && !sameAsDefault) {
          next.set('status', stored.statuses.join(','))
        }
        if (stored.priorities.length > 0) next.set('priority', stored.priorities.join(','))
        if (stored.projectId) next.set('project_id', stored.projectId)
        if (stored.sprintId) next.set('sprint_id', stored.sprintId)
        if (stored.epicId) next.set('epic_id', stored.epicId)
        if (stored.tagSlug) next.set('tag', stored.tagSlug)
        if (stored.mode && stored.mode !== 'all') next.set('mode', stored.mode)
        return next
      },
      { replace: true }
    )
    setHydrated(true)
  }, [location.key, searchParams, setSearchParams])

  // Persist current filter state to localStorage whenever it changes. Gated
  // on hydration so the initial pre-hydrate render doesn't write default
  // values over the stored filters before they've been restored.
  useEffect(() => {
    if (!hydrated) return
    saveOpsFilters({
      statuses: activeStatuses,
      priorities: activePriorities,
      projectId,
      sprintId,
      epicId,
      tagSlug,
      mode,
    })
  }, [hydrated, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, mode])

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
    if (!hydrated) return
    setLoading(true)
    fetchTasks()
  }, [hydrated, fetchTasks])

  // Refresh on SSE events
  useEffect(() => {
    if (!hydrated) return
    if (lastEvent) fetchTasks()
  }, [hydrated, lastEvent, fetchTasks])

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
    tagSlug !== null ||
    mode !== 'all'

  const emptyVariant = filtersActive ? 'no-results' : 'no-tasks'

  function handleClearFilters() {
    clearOpsFilters()
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        for (const k of FILTER_PARAM_KEYS) next.delete(k)
        return next
      },
      { replace: true }
    )
  }

  const cursorFilter = useMemo(
    () => ({
      statuses: activeStatuses,
      priorities: activePriorities,
      projectId,
      sprintId,
      epicId,
      tagSlug,
      mode,
    }),
    [activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, mode]
  )

  const handleVisibleOrderChange = useCallback(
    (ids: string[]) => saveTaskListCursor(ids, cursorFilter),
    [cursorFilter]
  )

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
      <PageHeader title="Operations">
        <RestartFrontendButton />
      </PageHeader>
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
      >
        {filtersActive && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleClearFilters}
            className="h-7 border-zinc-700 bg-zinc-900/50 px-2 text-[10px] uppercase tracking-wider text-zinc-300 hover:border-zinc-500 hover:text-zinc-100"
          >
            Clear filters
          </Button>
        )}
      </FilterBar>

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
            onVisibleOrderChange={handleVisibleOrderChange}
          />
        )}
      </div>
    </div>
  )
}
