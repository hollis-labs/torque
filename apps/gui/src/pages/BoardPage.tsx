import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useSearchParams, useLocation, useNavigate } from 'react-router-dom'
import { Skeleton, PageHeader, SummaryCards, EmptyState, Button } from '@hollis-labs/sysop-ui'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable } from '@/components/domain/task-table'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { SprintCreateDialog } from '@/components/domain/sprint-create-dialog'
import { TagCreateDialog } from '@/components/domain/tag-create-dialog'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { SchedulerToggleButton } from '@/components/domain/scheduler-toggle-button'
import { ScopeManagerDialog } from '@/components/domain/scope-manager-dialog'
import { useApi } from '@/hooks/use-api'
import { useSSE } from '@/hooks/use-sse'
import { notifyError } from '@/lib/toast'
import { DEFAULT_ACTIVE_STATUSES, TASK_STATUSES } from '@/lib/constants'
import { saveTaskListCursor } from '@/lib/task-list-cursor'
import {
  saveOpsFilters,
  readOpsFilters,
  clearOpsFilters,
  parseManualFilter,
  type ManualFilter,
  type OpsFilters,
} from '@/lib/ops-filters-storage'
import { FolderTree } from 'lucide-react'
import type { Epic, FeatureFlags, Project, Sprint, Tag, Task, TaskStatus } from '@/lib/types'

const FILTER_PARAM_KEYS = ['status', 'priority', 'project_id', 'sprint_id', 'epic_id', 'tag', 'manual'] as const

const SSE_EVENTS = ['task.created', 'task.updated', 'task.transitioned']

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
  const navigate = useNavigate()
  const scrollContainerRef = useRef<HTMLDivElement>(null)

  // Filter state derived from URL (URL is source of truth for round-tripping)
  const activeStatuses = useMemo(
    () => parseStatusParam(searchParams.get('status')),
    [searchParams]
  )
  const activePriorities = useMemo(
    () => parsePriorityParam(searchParams.get('priority')),
    [searchParams]
  )
  const manualFilter: ManualFilter = useMemo(
    () => parseManualFilter(searchParams.get('manual')),
    [searchParams]
  )
  const projectId = searchParams.get('project_id')
  const sprintId = searchParams.get('sprint_id')
  const epicId = searchParams.get('epic_id')
  const tagSlug = searchParams.get('tag')

  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState<string>('')
  // System (kind=internal) toggle — storage-only, default off. Surfacing
  // automation tasks (Reviewer end-agents etc.) is an admin/diagnostic
  // workflow, not a routine deep-link target, so we don't URL-back it.
  // CW-20260503-0011.
  const [includeInternal, setIncludeInternal] = useState<boolean>(false)

  // Group picker data
  const [projects, setProjects] = useState<Project[]>([])
  const [sprints, setSprints] = useState<Sprint[]>([])
  const [epics, setEpics] = useState<Epic[]>([])
  const [tags, setTags] = useState<Tag[]>([])
  const [flags, setFlags] = useState<FeatureFlags>({ projects: false, epics: false, sprints: false })

  // Total rows matching the current filter+search from the list endpoint —
  // authoritative for the summary's "M matches" display. Distinct from
  // `tasks.length`, which reflects the windowed/capped payload.
  const [totalMatchCount, setTotalMatchCount] = useState<number>(0)

  // Create-modal state
  const [projectCreateOpen, setProjectCreateOpen] = useState(false)
  const [epicCreateOpen, setEpicCreateOpen] = useState(false)
  const [sprintCreateOpen, setSprintCreateOpen] = useState(false)
  const [tagCreateOpen, setTagCreateOpen] = useState(false)
  const [scopeManagerOpen, setScopeManagerOpen] = useState(false)

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
  // `location.key` is unique per history entry. The `hydrated` state gates
  // downstream effects (save, fetch) so they skip the very first pre-
  // hydrate render on a fresh mount.
  //
  // CW-20260418-0032: two complementary guards below together prevent the
  // replaceState loop observed 2026-04-18.
  //
  // 1. Effect deps are ONLY `[location.key]`. Including `searchParams` or
  //    `setSearchParams` would fire the effect on every render because
  //    `useSearchParams` returns new object references each render.
  //
  // 2. The effect bails out before calling `setSearchParams` when the
  //    computed URL matches the current URL byte-for-byte. This matters
  //    because `setSearchParams(..., { replace: true })` commits a new
  //    history entry (new location.key) even when the URL content is
  //    unchanged — the old comment here claiming it "keeps the same key"
  //    was wrong, and that wrongness was the actual loop trigger: a
  //    no-op replace would mint a new key, the effect would re-run under
  //    `[location.key]`, produce the same no-op URL, and repeat forever.
  //    The common trip wire is stored filters that equal DEFAULT_ACTIVE_
  //    STATUSES (the default case): the target URL and the current URL
  //    are both empty, so the no-op check is load-bearing.
  const [hydrated, setHydrated] = useState(false)
  const lastHydratedKey = useRef<string | null>(null)
  useEffect(() => {
    if (lastHydratedKey.current === location.key) return
    lastHydratedKey.current = location.key

    // Read storage first. Search is storage-only (never URL-backed), so it
    // must hydrate even when URL filters are present — otherwise navigating
    // back to /operations with persisted URL filters would silently wipe the
    // user's search. URL-backed fields (status, priority, etc.) still defer
    // to the URL via the `urlHasFilter` short-circuit below.
    const stored = readOpsFilters()
    if (stored?.search) setSearch(stored.search)
    if (stored?.includeInternal) setIncludeInternal(true)

    const urlHasFilter = FILTER_PARAM_KEYS.some((k) => searchParams.has(k))
    if (urlHasFilter) {
      setHydrated(true)
      return
    }
    if (!stored) {
      setHydrated(true)
      return
    }

    // Compute the target params up-front so we can compare vs. the current
    // URL and bail out on a no-op. See guard #2 in the block comment above.
    const next = new URLSearchParams(searchParams)
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
    if (stored.manual && stored.manual !== 'both') next.set('manual', stored.manual)

    if (next.toString() === searchParams.toString()) {
      // No-op — don't call setSearchParams. A replace with unchanged URL
      // would still mint a new location.key and re-fire the effect; the
      // identity check on the line above is the circuit-breaker.
      setHydrated(true)
      return
    }

    setSearchParams(next, { replace: true })
    setHydrated(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.key])

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
      manual: manualFilter,
      search,
      includeInternal,
    })
  }, [hydrated, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search, includeInternal])

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

  useEffect(() => {
    let cancelled = false
    void api.getFeatureFlags()
      .then((next) => {
        if (!cancelled) setFlags(next)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [api])

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

  // Monotonic request id so a slow older fetch can't overwrite a fast newer
  // one. The filter-change and SSE-refresh effects both call fetchTasks, so
  // multiple responses can be in flight; only the latest should mutate state.
  const fetchGeneration = useRef(0)

  const fetchTasks = useCallback(async () => {
    const myGen = ++fetchGeneration.current
    try {
      const result = await api.listTasks({
        status: activeStatuses,
        priority: activePriorities.length ? activePriorities : undefined,
        project_id: projectId ?? undefined,
        sprint_id: sprintId ?? undefined,
        epic_id: epicId ?? undefined,
        tags: tagSlug ? [tagSlug] : undefined,
        manual: manualFilter === 'both' ? undefined : manualFilter === 'manual',
        search: search || undefined,
        include_internal: includeInternal || undefined,
      })
      if (myGen !== fetchGeneration.current) return
      setTasks(result.tasks)
      setTotalMatchCount(result.total)
      setError(null)
    } catch (err) {
      if (myGen !== fetchGeneration.current) return
      setError(err instanceof Error ? err.message : 'Failed to load tasks')
    } finally {
      if (myGen === fetchGeneration.current) {
        setLoading(false)
      }
    }
  }, [api, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search, includeInternal])

  // Refetch when filters/search change. Intentionally does NOT setLoading(true)
  // — the initial useState(true) covers the first-mount skeleton; subsequent
  // fetches keep the current rows visible and swap data in place when the
  // response lands, so filter/search changes feel instant instead of flashing
  // the skeleton.
  useEffect(() => {
    if (!hydrated) return
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

  // Sync localStorage with the user's intent BEFORE setSearchParams. The
  // rehydrate effect (line 141) fires on the resulting location.key change
  // and reads storage; without this synchronous write it would observe the
  // previous save effect's stale value and undo a toggle that drops the
  // URL back to all-defaults (e.g. re-toggling the only off-default chip).
  // The save effect at line 195 still runs as the safety net for code paths
  // that don't go through these handlers.
  function persistFilters(overrides: Partial<OpsFilters>) {
    saveOpsFilters({
      statuses: activeStatuses,
      priorities: activePriorities,
      projectId,
      sprintId,
      epicId,
      tagSlug,
      manual: manualFilter,
      search,
      includeInternal,
      ...overrides,
    })
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
    persistFilters({ statuses: next })
    setStatusList(next)
  }

  function handlePriorityToggle(priority: number) {
    const next = activePriorities.includes(priority)
      ? activePriorities.filter((p) => p !== priority)
      : [...activePriorities, priority]
    persistFilters({ priorities: next })
    updateParams((p) => {
      if (next.length === 0) p.delete('priority')
      else p.set('priority', next.join(','))
    })
  }

  function handleGroupChange(key: 'project_id' | 'sprint_id' | 'epic_id' | 'tag', value: string | null) {
    const overrides: Partial<OpsFilters> = {}
    if (key === 'project_id') overrides.projectId = value
    else if (key === 'sprint_id') overrides.sprintId = value
    else if (key === 'epic_id') overrides.epicId = value
    else if (key === 'tag') overrides.tagSlug = value
    persistFilters(overrides)
    updateParams((p) => {
      if (value === null) p.delete(key)
      else p.set(key, value)
    })
  }

  function handleManualFilterChange(value: ManualFilter) {
    persistFilters({ manual: value })
    updateParams((p) => {
      if (value === 'both') p.delete('manual')
      else p.set('manual', value)
    })
  }

  function handleIncludeInternalChange(value: boolean) {
    setIncludeInternal(value)
    persistFilters({ includeInternal: value })
  }

  async function handleTransition(id: string, status: TaskStatus) {
    try {
      await api.transitionTask(id, status)
      fetchTasks()
    } catch (err) {
      notifyError(err, 'Failed to update task status')
    }
  }

  const activeFilterCount =
    (activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ? 1 : 0) +
    (activePriorities.length > 0 ? 1 : 0) +
    (manualFilter !== 'both' ? 1 : 0) +
    (includeInternal ? 1 : 0) +
    (projectId !== null ? 1 : 0) +
    (sprintId !== null ? 1 : 0) +
    (epicId !== null ? 1 : 0) +
    (tagSlug !== null ? 1 : 0)

  const searchMatchCount = search ? totalMatchCount : undefined

  const emptyVariant = activeFilterCount > 0 || search.length > 0 ? 'no-results' : 'no-tasks'

  function handleClearFilters() {
    clearOpsFilters()
    setSearch('')
    setIncludeInternal(false)
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
      manual: manualFilter,
      search,
    }),
    [activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search]
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
        {flags.projects && (
          <Button variant="outline" size="sm" onClick={() => navigate('/projects')}>
            Projects
          </Button>
        )}
        {flags.sprints && (
          <Button variant="outline" size="sm" onClick={() => navigate('/sprints')}>
            Sprints
          </Button>
        )}
        {flags.epics && (
          <Button variant="outline" size="sm" onClick={() => navigate('/epics')}>
            Epics
          </Button>
        )}
        {(flags.projects || flags.epics || flags.sprints) && (
          <Button variant="outline" size="sm" onClick={() => setScopeManagerOpen(true)}>
            <FolderTree className="h-3.5 w-3.5" />
            Scope
          </Button>
        )}
        <SchedulerToggleButton />
        <RestartFrontendButton />
      </PageHeader>
      {!loading && !error && <SummaryCards cards={summaryCards} />}
      <FilterBar
        activeStatuses={activeStatuses}
        onStatusToggle={handleStatusToggle}
        activePriorities={activePriorities}
        onPriorityToggle={handlePriorityToggle}
        manualFilter={manualFilter}
        onManualFilterChange={handleManualFilterChange}
        includeInternal={includeInternal}
        onIncludeInternalChange={handleIncludeInternalChange}
        projects={projects}
        projectId={projectId}
        onProjectChange={(id) => handleGroupChange('project_id', id)}
        onProjectCreate={() => setProjectCreateOpen(true)}
        sprints={visibleSprints}
        sprintId={sprintId}
        onSprintChange={(id) => handleGroupChange('sprint_id', id)}
        onSprintCreate={() => setSprintCreateOpen(true)}
        epics={visibleEpics}
        epicId={epicId}
        onEpicChange={(id) => handleGroupChange('epic_id', id)}
        onEpicCreate={() => setEpicCreateOpen(true)}
        tags={tags}
        tagSlug={tagSlug}
        onTagChange={(slug) => handleGroupChange('tag', slug)}
        onTagCreate={() => setTagCreateOpen(true)}
        searchQuery={search}
        onSearchChange={setSearch}
        searchMatchCount={searchMatchCount}
        activeFilterCount={activeFilterCount}
        onClear={handleClearFilters}
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
      <SprintCreateDialog
        open={sprintCreateOpen}
        onOpenChange={setSprintCreateOpen}
        projects={projects}
        defaultProjectId={projectId}
        onCreated={(sprint) => {
          refreshPickers()
          handleGroupChange('sprint_id', sprint.id)
        }}
      />
      <TagCreateDialog
        open={tagCreateOpen}
        onOpenChange={setTagCreateOpen}
        onCreated={(tag) => {
          refreshPickers()
          handleGroupChange('tag', tag.slug)
        }}
      />
      <ScopeManagerDialog
        open={scopeManagerOpen}
        onOpenChange={setScopeManagerOpen}
        flags={flags}
        onDataChange={() => {
          refreshPickers()
          fetchTasks()
        }}
      />
      <div ref={scrollContainerRef} className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : error ? (
          <EmptyState
            variant="error"
            title="Something went wrong"
            description={error}
            action={{ label: 'Retry', onClick: fetchTasks }}
          />
        ) : (
          <TaskTable
            tasks={tasks}
            onTransition={handleTransition}
            onTaskChange={(updated) => {
              // Optimistic in-place patch keeps the row visible during the
              // round-trip; fetchTasks() then reconciles with the active
              // filter so a row pushed outside the filter (e.g. transitioned
              // to done while filtering on todo) drops out of the list.
              setTasks((prev) => prev.map((t) => (t.id === updated.id ? updated : t)))
              fetchTasks()
            }}
            onTaskDelete={(id) => setTasks((prev) => prev.filter((t) => t.id !== id))}
            emptyVariant={emptyVariant}
            onVisibleOrderChange={handleVisibleOrderChange}
            scrollRootRef={scrollContainerRef}
          />
        )}
      </div>
    </div>
  )
}
