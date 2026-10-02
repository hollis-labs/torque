import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useSearchParams, useLocation, useNavigate } from 'react-router-dom'
import { Skeleton, PageHeader, SummaryCards, EmptyState, Button } from '@hollis-labs/sysop-ui'
import { FilterBar } from '@/components/domain/filter-bar'
import { TaskTable, type TaskTableSort } from '@/components/domain/task-table'
import { ProjectCreateDialog } from '@/components/domain/project-create-dialog'
import { EpicCreateDialog } from '@/components/domain/epic-create-dialog'
import { SprintCreateDialog } from '@/components/domain/sprint-create-dialog'
import { TagCreateDialog } from '@/components/domain/tag-create-dialog'
import { RestartFrontendButton } from '@/components/domain/restart-frontend-button'
import { SchedulerToggleButton } from '@/components/domain/scheduler-toggle-button'
import { ScopeManagerDialog } from '@/components/domain/scope-manager-dialog'
import { useApi } from '@/hooks/use-api'
import { useDebouncedCallback } from '@/hooks/use-debounced-callback'
import { usePagedList, type ListChange, type PageRequest } from '@/hooks/use-paged-list'
import { OpsSavedViews } from '@/components/domain/ops-saved-views'
import { readOpsViews, saveOpsViews, readActiveOpsView, saveActiveOpsView } from '@/lib/ops-saved-views'
import type { TaskFacetResult } from '@/lib/api'
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
import type { FeatureFlags, Tag, TagColor, TaskFilter, TaskSummary, TaskStatus } from '@/lib/types'

const FILTER_PARAM_KEYS = ['status', 'priority', 'project_id', 'sprint_id', 'epic_id', 'tag', 'manual', 'eligible', 'sort_by', 'sort_dir'] as const

type BoardParams = Omit<TaskFilter, 'cursor' | 'offset'>
const SORT_FIELDS = ['priority', 'status', 'updated_at', 'created_at'] as const

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
  const eligibleOnly = searchParams.get('eligible') === '1'
  const sortBy: TaskTableSort = SORT_FIELDS.find(field => field === searchParams.get('sort_by')) ?? 'updated_at'
  const sortDir = searchParams.get('sort_dir') === 'asc' ? 'asc' : 'desc'
  const [views, setViews] = useState(readOpsViews)
  const [activeViewId, setActiveViewId] = useState(readActiveOpsView)

  const [search, setSearch] = useState<string>('')
  // System (kind=internal) toggle — storage-only, default off. Surfacing
  // automation tasks (Reviewer end-agents etc.) is an admin/diagnostic
  // workflow, not a routine deep-link target, so we don't URL-back it.
  // CW-20260503-0011.
  const [includeInternal, setIncludeInternal] = useState<boolean>(false)

  // Group picker data
  const [tagQuery, setTagQuery] = useState('')
  const [serverTagQuery, setServerTagQuery] = useState('')
  const [tagColor, setTagColor] = useState<TagColor | ''>('')
  const [selectedTag, setSelectedTag] = useState<Tag | null>(null)
  const scheduleTagQuery = useDebouncedCallback(() => setServerTagQuery(tagQuery), 300)
  useEffect(() => { scheduleTagQuery() }, [tagQuery, scheduleTagQuery])
  const tagPage = usePagedList({
    params: { query: serverTagQuery || undefined, color: tagColor || undefined, limit: 50 },
    fetchPage: ({ params, cursor, signal }) => api.listTags({ ...params, cursor }, signal),
    getId: (tag: Tag) => tag.slug,
  })
  useEffect(() => {
    if (!tagSlug) return
    const abort = new AbortController()
    void api.getTag(tagSlug).then(tag => { if (!abort.signal.aborted) setSelectedTag(tag) }).catch(() => {})
    return () => abort.abort()
  }, [api, tagSlug])
  const [flags, setFlags] = useState<FeatureFlags>({ projects: false, epics: false, sprints: false })

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
    if (stored.eligibleOnly) next.set('eligible', '1')
    if (stored.sortBy) next.set('sort_by', stored.sortBy)
    if (stored.sortDir) next.set('sort_dir', stored.sortDir)

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
      ...(eligibleOnly ? { eligibleOnly: true } : {}),
      sortBy,
      sortDir,
    })
  }, [hydrated, activeStatuses, activePriorities, projectId, sprintId, epicId, tagSlug, manualFilter, search, includeInternal, eligibleOnly, sortBy, sortDir])

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

  const [serverSearch, setServerSearch] = useState(() => readOpsFilters()?.search.trim() ?? '')
  const scheduleSearch = useDebouncedCallback(() => setServerSearch(search.trim()), 300)
  useEffect(() => { scheduleSearch() }, [search, scheduleSearch])
  const rawFilters: BoardParams = eligibleOnly ? { eligible: true } : {
    status: activeStatuses,
    priority: activePriorities.length ? activePriorities : undefined,
    project_id: projectId ?? undefined,
    sprint_id: sprintId ?? undefined,
    epic_id: epicId ?? undefined,
    tags: tagSlug ? [tagSlug] : undefined,
    manual: manualFilter === 'both' ? undefined : manualFilter === 'manual',
    search: serverSearch || undefined,
    include_internal: includeInternal || undefined,
  }
  const cohortKey = JSON.stringify(rawFilters)
  const filters = useMemo(() => JSON.parse(cohortKey) as BoardParams, [cohortKey])
  const listQueryKey = JSON.stringify({ ...filters, sort_by: sortBy, sort_dir: sortDir })
  const facetRequest = useRef<AbortController | null>(null)
  const facetGeneration = useRef(0)
  const [facetState, setFacetState] = useState<{ key: string; data?: TaskFacetResult; error?: string }>({ key: '' })
  const fetchFacets = useCallback(async () => {
    const gen = ++facetGeneration.current
    facetRequest.current?.abort()
    const abort = new AbortController()
    facetRequest.current = abort
    try {
      const data = await api.taskFacets(filters, 'status', abort.signal)
      if (!abort.signal.aborted && gen === facetGeneration.current) setFacetState({ key: cohortKey, data })
    } catch (error) {
      if (!abort.signal.aborted && gen === facetGeneration.current) setFacetState({ key: cohortKey, error: error instanceof Error ? error.message : 'Failed to load counts' })
    }
  }, [api, filters, cohortKey])
  useEffect(() => {
    if (!hydrated) return
    // Synchronize the remote facet query; its error path may complete immediately.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void fetchFacets()
    return () => { facetRequest.current?.abort() }
  }, [hydrated, fetchFacets])
  const facets = facetState.key === cohortKey ? facetState.data : undefined
  const countsError = facetState.key === cohortKey ? facetState.error : undefined

  const visibleIds = useRef(new Set<string>())
  const opaqueQuery = useRef({ key: listQueryKey, enabled: false })
  const [staleMembershipKey, setStaleMembershipKey] = useState<string | null>(null)
  const [membershipQueryKey, setMembershipQueryKey] = useState(listQueryKey)
  if (membershipQueryKey !== listQueryKey) {
    setMembershipQueryKey(listQueryKey)
    setStaleMembershipKey(null)
  }
  useEffect(() => { opaqueQuery.current = { key: listQueryKey, enabled: Boolean(filters.eligible || filters.search) } }, [listQueryKey, filters])
  const subscribe = useCallback((onChange: (change: ListChange<TaskSummary>) => void) => api.subscribeEvents(event => {
    if (!['task.created', 'task.updated', 'task.transitioned', 'task.deleted'].includes(event.type)) return
    const id = event.data.task_id
    if (typeof id !== 'string') return
    if (visibleIds.current.has(id) && opaqueQuery.current.enabled) setStaleMembershipKey(opaqueQuery.current.key)
    const status = event.type === 'task.transitioned' && typeof event.data.status === 'string'
      ? event.data.status as TaskStatus : undefined
    onChange({ id, patch: status ? { status } : undefined, remove: event.type === 'task.deleted',
      refresh: !status && event.type !== 'task.deleted' })
  }), [api])
  const fetchPage = useCallback(({ params, cursor, signal }: PageRequest<BoardParams>) =>
    api.listTaskSummaryPage({ ...params, cursor }, signal), [api])
  const fetchItem = useCallback((id: string | number, { signal }: { signal: AbortSignal }) =>
    api.getTask(String(id), signal).catch(error => {
      if (error instanceof Error && 'status' in error && error.status === 404) return null
      throw error
    }), [api])
  const page = usePagedList({ fetchPage, fetchItem, params: { ...filters, limit: 50, sort_by: sortBy, sort_dir: sortDir },
    getId: (task: TaskSummary) => task.id, enabled: hydrated, subscribe, onInvalidate: fetchFacets,
    matches: (task: TaskSummary) => (!filters.status?.length || filters.status.includes(task.status))
      && (!filters.priority?.length || filters.priority.includes(task.priority))
      && (!filters.project_id || task.project_id === filters.project_id)
      && (!filters.epic_id || task.epic_id === filters.epic_id)
      && (!filters.sprint_id || task.sprint_id === filters.sprint_id)
      && (filters.manual === undefined || task.manual === filters.manual)
      && (filters.include_internal || task.kind !== 'internal')
      && (!filters.tags?.length || filters.tags.every(slug => task.tags?.some(tag => tag.slug === slug))) })
  const tasks = page.items
  const loading = page.loading && tasks.length === 0
  const error = page.error?.message
  useEffect(() => { visibleIds.current = new Set(tasks.map(task => task.id)) }, [tasks])
  const refreshPage = page.refresh
  const refresh = useCallback(() => {
    setStaleMembershipKey(null)
    void refreshPage()
    void fetchFacets()
  }, [refreshPage, fetchFacets])

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
      ...(eligibleOnly ? { eligibleOnly: true } : {}),
      sortBy,
      sortDir,
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
    if (key === 'project_id') { overrides.projectId = value; overrides.epicId = null; overrides.sprintId = null }
    else if (key === 'sprint_id') overrides.sprintId = value
    else if (key === 'epic_id') overrides.epicId = value
    else if (key === 'tag') overrides.tagSlug = value
    persistFilters(overrides)
    updateParams((p) => {
      if (key === 'project_id') { p.delete('sprint_id'); p.delete('epic_id') }
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
      const task = await api.transitionTask(id, status)
      page.applyEvent({ id, item: task })
    } catch (err) {
      notifyError(err, 'Failed to update task status')
    }
  }

  const activeFilterCount = eligibleOnly ? 1 :
    (activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ? 1 : 0) +
    (activePriorities.length > 0 ? 1 : 0) +
    (manualFilter !== 'both' ? 1 : 0) +
    (includeInternal ? 1 : 0) +
    (projectId !== null ? 1 : 0) +
    (sprintId !== null ? 1 : 0) +
    (epicId !== null ? 1 : 0) +
    (tagSlug !== null ? 1 : 0)

  const searchMatchCount = search && search.trim() === serverSearch ? facets?.matching_count : undefined

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

  const workingFilters: OpsFilters = { statuses: activeStatuses, priorities: activePriorities, projectId, sprintId, epicId,
    tagSlug, manual: manualFilter, search, includeInternal, ...(eligibleOnly ? { eligibleOnly: true } : {}), sortBy, sortDir }
  const activeView = views.find(view => view.id === activeViewId)
  function filterKey(filters: OpsFilters) {
    const normalized = { ...filters, eligibleOnly: Boolean(filters.eligibleOnly), sortBy: filters.sortBy ?? 'updated_at', sortDir: filters.sortDir ?? 'desc' }
    return JSON.stringify(Object.fromEntries(Object.entries(normalized).sort(([a], [b]) => a.localeCompare(b))))
  }
  const viewModified = !activeView || filterKey(activeView.filters) !== filterKey(workingFilters)
  function selectView(id: string) { setActiveViewId(id); saveActiveOpsView(id) }
  function applyFilters(filters: OpsFilters) {
    saveOpsFilters(filters)
    setSearch(filters.search)
    setIncludeInternal(filters.includeInternal)
    updateParams(params => {
      FILTER_PARAM_KEYS.forEach(key => params.delete(key))
      const defaultStatuses = filters.statuses.length === DEFAULT_ACTIVE_STATUSES.length && DEFAULT_ACTIVE_STATUSES.every(status => filters.statuses.includes(status))
      if (!defaultStatuses) params.set('status', filters.statuses.join(','))
      if (filters.priorities.length) params.set('priority', filters.priorities.join(','))
      if (filters.projectId) params.set('project_id', filters.projectId)
      if (filters.sprintId) params.set('sprint_id', filters.sprintId)
      if (filters.epicId) params.set('epic_id', filters.epicId)
      if (filters.tagSlug) params.set('tag', filters.tagSlug)
      if (filters.manual !== 'both') params.set('manual', filters.manual)
      if (filters.eligibleOnly) params.set('eligible', '1')
      if (filters.sortBy) params.set('sort_by', filters.sortBy)
      if (filters.sortDir) params.set('sort_dir', filters.sortDir)
    })
  }
  function applyView() { if (activeView) applyFilters(activeView.filters) }
  function saveViewAs(name: string) {
    const view = { id: crypto.randomUUID(), name, filters: workingFilters }
    const next = [...views, view]
    setViews(next); saveOpsViews(next); selectView(view.id)
  }
  function saveViewOver() {
    const next = views.map(view => view.id === activeViewId ? { ...view, filters: workingFilters } : view)
    setViews(next); saveOpsViews(next)
  }
  function handleSortChange(sort: TaskTableSort, dir: 'asc' | 'desc') {
    persistFilters({ sortBy: sort, sortDir: dir })
    updateParams(params => { params.set('sort_by', sort); params.set('sort_dir', dir) })
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

  const statusCounts = Object.fromEntries((facets?.facets.find(facet => facet.dimension === 'status')?.buckets ?? [])
    .map(bucket => [String(bucket.value), bucket.count]))
  const openCount = (statusCounts.backlog ?? 0) + (statusCounts.todo ?? 0) + (statusCounts.queued ?? 0)
  const doingCount = statusCounts.doing ?? 0
  const reviewCount = statusCounts.review ?? 0
  const blockedCount = statusCounts.blocked ?? 0

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
      {facets && <SummaryCards cards={summaryCards} />}
      {countsError && <p role="alert" className="px-4 py-2 text-xs text-red-400">{countsError} <button onClick={() => void fetchFacets()}>Retry counts</button></p>}
      {(page.isStale || staleMembershipKey === listQueryKey) && <div className="flex items-center gap-2 px-4 py-2 text-xs text-text-soft">
        <Button variant="outline" size="sm" onClick={refresh}>Updated — refresh</Button>
      </div>}
      <FilterBar
        activeViewName={activeView ? `${activeView.name}${viewModified ? ' *' : ''}` : undefined}
        eligibleOnly={eligibleOnly}
        onEligibleChange={(value) => {
          persistFilters({ eligibleOnly: value })
          updateParams(params => { if (value) params.set('eligible', '1'); else params.delete('eligible') })
        }}
        editorControls={<OpsSavedViews views={views} activeId={activeViewId} onSelect={selectView}
          onApply={applyView} onReset={() => activeView ? applyFilters(activeView.filters) : handleClearFilters()}
          onSaveAs={saveViewAs} onSaveOver={saveViewOver} modified={viewModified} />}
        activeStatuses={activeStatuses}
        onStatusToggle={handleStatusToggle}
        activePriorities={activePriorities}
        onPriorityToggle={handlePriorityToggle}
        manualFilter={manualFilter}
        onManualFilterChange={handleManualFilterChange}
        includeInternal={includeInternal}
        onIncludeInternalChange={handleIncludeInternalChange}

        projectId={projectId}
        onProjectChange={(id) => handleGroupChange('project_id', id)}
        onProjectCreate={() => setProjectCreateOpen(true)}

        sprintId={sprintId}
        onSprintChange={(id) => handleGroupChange('sprint_id', id)}
        onSprintCreate={() => setSprintCreateOpen(true)}

        epicId={epicId}
        onEpicChange={(id) => handleGroupChange('epic_id', id)}
        onEpicCreate={() => setEpicCreateOpen(true)}
        tags={tagPage.items}
        selectedTag={selectedTag?.slug === tagSlug ? selectedTag : null}
        tagQuery={tagQuery} onTagQueryChange={setTagQuery} tagColor={tagColor} onTagColorChange={setTagColor}
        onTagMore={tagPage.meta?.has_more ? () => void tagPage.loadMore() : undefined}
        tagLoading={tagPage.loading} tagError={tagPage.error?.message} onTagRetry={() => void tagPage.reload()}
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

          handleGroupChange('project_id', p.id)
        }}
      />
      <EpicCreateDialog
        open={epicCreateOpen}
        onOpenChange={setEpicCreateOpen}

        defaultProjectId={projectId}
        onCreated={(e) => {

          handleGroupChange('epic_id', e.id)
        }}
      />
      <SprintCreateDialog
        open={sprintCreateOpen}
        onOpenChange={setSprintCreateOpen}

        defaultProjectId={projectId}
        onCreated={(sprint) => {

          handleGroupChange('sprint_id', sprint.id)
        }}
      />
      <TagCreateDialog
        open={tagCreateOpen}
        onOpenChange={setTagCreateOpen}
        onCreated={(tag) => {
          void tagPage.reload()
          handleGroupChange('tag', tag.slug)
        }}
      />
      <ScopeManagerDialog
        open={scopeManagerOpen}
        onOpenChange={setScopeManagerOpen}
        flags={flags}
        onDataChange={() => {

          void fetchFacets()
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
            action={{ label: 'Retry', onClick: refresh }}
          />
        ) : (
          <TaskTable
            tasks={tasks}
            onTransition={handleTransition}
            onTaskChange={(updated) => page.applyEvent({ id: updated.id, item: updated })}
            onTaskDelete={(id) => page.applyEvent({ id, remove: true })}
            hasMore={page.hasMore}
            loadingMore={page.loadingMore}
            onLoadMore={page.loadMore}
            sortBy={sortBy}
            sortDir={sortDir}
            onSortChange={handleSortChange}
            emptyVariant={emptyVariant}
            onVisibleOrderChange={handleVisibleOrderChange}
            scrollRootRef={scrollContainerRef}
          />
        )}
      </div>
    </div>
  )
}
