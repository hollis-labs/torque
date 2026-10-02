import type {
  Task,
  ListPage,
  ListQuery,
  TaskFilter,
  TaskScopeKey,
  TaskScopeRollupResponse,
  TaskSummary,
  Run,
  Artifact,
  Collection,
  Comment,
  SSEEvent,
  FeatureFlags,
  Project,
  ProjectArtifact,
  Sprint,
  Epic,
  Tag,
  TagColor,
  Template,
  TemplateInstantiateRequest,
  Checkpoint,
  CheckpointEmitRequest,
  HITLWorkflowDefinition,
  HITLWorkflowListResponse,
  Subtodo,
  PlanDetail,
  PlanPhaseInput,
  SchedulerStatus,
  ModelEntry,
  MessageEnvelope,
  SendMessageRequest,
  MessageFilter,
  BrokerRequest,
} from './types'

export interface RunQuery {
  executor?: string
  profile?: string
  task_id?: string
  project_id?: string
  sprint_id?: string
  epic_id?: string
  status?: string
  since?: string
  until?: string
  limit?: number
  offset?: number
  cursor?: string
  sort_by?: 'started_at' | 'status' | 'duration' | 'cost'
  sort_dir?: 'asc' | 'desc'
  include_total?: boolean
}

export type RunPage = ListPage<Run>

export interface AggregateFacet {
  dimension: string
  buckets: { value: string | number | boolean | null; count: number }[]
  total_distinct: number
  returned: number
  truncated: boolean
}
export interface TaskFacetResult {
  matching_count: number
  facets: AggregateFacet[]
}
export interface ParentQuery extends Omit<ListQuery, 'cursor' | 'offset'> {
  status?: string
  project_id?: string
  include_archived?: boolean
}
export interface ParentFacetResult extends TaskFacetResult {
  task_totals: { total: number; counts: Record<string, number> }
  task_rollups: {
    scopes: { scope_id: string; total: number; counts: Record<string, number>; children?: { sprints: number; epics: number } }[]
    total_distinct: number; returned: number; truncated: boolean
  }
  child_totals?: { sprints: number; epics: number }
}
export interface RunFacetResult extends TaskFacetResult {
  totals: { cost: number; prompt_tokens: number; completion_tokens: number }
}
export interface RunTimeBucket {
  start: string
  count: number
  prompt_tokens: number
  completion_tokens: number
  cost: number
  status_counts: Record<string, number>
}
export interface RunTimeSeries {
  bucket: 'hour' | 'day'
  tz_offset_minutes: number
  since: string
  until: string
  buckets: RunTimeBucket[]
  totals: Omit<RunTimeBucket, 'start' | 'status_counts'>
}

export class ApiError extends Error {
  status: number
  url?: string
  constructor(status: number, message: string, url?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.url = url
  }
}

function isHtmlResponse(res: Response, text: string): boolean {
  const contentType = res.headers.get('Content-Type')?.toLowerCase() ?? ''
  return contentType.includes('text/html') || /^\s*<!doctype html/i.test(text) || /^\s*<html/i.test(text)
}

function htmlRouteMessage(res: Response): string {
  const path = (() => {
    try {
      return new URL(res.url).pathname
    } catch {
      return res.url || 'unknown route'
    }
  })()
  return `API returned HTML instead of JSON for ${path}. This usually means the backend route is missing or the SPA/dev server handled the request.`
}

export function isHtmlApiFallbackError(err: unknown): err is ApiError {
  return err instanceof ApiError && /returned HTML instead of JSON/i.test(err.message)
}

/**
 * Shape of a run row as the backend currently serializes it — fields come
 * straight off the sqlstore struct, so they use PascalCase and wrap nullable
 * columns in sql.Null* envelopes. We normalize to the flat snake_case `Run`
 * type the UI expects.
 */
interface ApiNullTime { Time: string; Valid: boolean }
interface ApiNullInt64 { Int64: number; Valid: boolean }
interface ApiNullString { String: string; Valid: boolean }

interface ApiRunRecord {
  ID: number
  TaskID: string
  Executor: string
  AgentProfile?: string
  Status: string
  StartedAt: string
  EndedAt: ApiNullTime | null
  PromptTokens: number
  CompletionTokens: number
  Cost: number
  ExitCode: ApiNullInt64 | null
  ErrorMessage: string
  Metadata?: ApiNullString | null
}

interface ApiArtifactRecord {
  ID: number
  TaskID: string
  RunID: ApiNullInt64 | null
  Type: string
  Content: string
  URL: string
  FilePath: string
  Metadata?: ApiNullString | null
  CreatedAt: string
}

function taskFilterParams(filter?: TaskFilter): Record<string, string | number | boolean | undefined> {
  const params: Record<string, string | number | boolean | undefined> = {}
  if (filter?.eligible !== undefined) params['eligible'] = filter.eligible
  if (filter?.status?.length) params['status'] = filter.status.join(',')
  if (filter?.priority?.length) params['priority'] = filter.priority.join(',')
  if (filter?.tags?.length) params['tags'] = filter.tags.join(',')
  if (filter?.sprint_id) params['sprint_id'] = filter.sprint_id
  if (filter?.project_id) params['project_id'] = filter.project_id
  if (filter?.epic_id) params['epic_id'] = filter.epic_id
  if (filter?.search) params['search'] = filter.search
  if (filter?.limit !== undefined) params['limit'] = filter.limit
  if (filter?.offset !== undefined) params['offset'] = filter.offset
  if (filter?.cursor !== undefined) params['cursor'] = filter.cursor
  if (filter?.include_total !== undefined) params['include_total'] = filter.include_total
  if (filter?.kind) params['kind'] = filter.kind
  if (filter?.parent_id !== undefined) params['parent_id'] = filter.parent_id
  if (filter?.manual !== undefined) params['manual'] = filter.manual ? 'true' : 'false'
  if (filter?.include_internal) params['include_internal'] = 'true'
  if (filter?.sort_by) params['sort_by'] = filter.sort_by
  if (filter?.sort_dir) params['sort_dir'] = filter.sort_dir
  return params
}

function normalizeArtifact(raw: ApiArtifactRecord): Artifact {
  let metadata: Record<string, unknown> | undefined
  if (raw.Metadata?.Valid && raw.Metadata.String) {
    try {
      const parsed = JSON.parse(raw.Metadata.String) as unknown
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        metadata = parsed as Record<string, unknown>
      }
    } catch {
      // Agent wrote a non-JSON string; treat as no structured metadata
      // instead of surfacing a parse error to the tab.
    }
  }
  return {
    id: raw.ID,
    task_id: raw.TaskID,
    run_id: raw.RunID?.Valid ? raw.RunID.Int64 : null,
    type: raw.Type ?? '',
    content: raw.Content ?? '',
    url: raw.URL ?? '',
    file_path: raw.FilePath ?? '',
    metadata,
    created_at: raw.CreatedAt,
  }
}

function normalizeRun(raw: ApiRunRecord): Run {
  return {
    id: raw.ID,
    task_id: raw.TaskID,
    executor: raw.Executor ?? '',
    agent_profile: raw.AgentProfile ?? '',
    status: raw.Status ?? '',
    prompt_tokens: raw.PromptTokens ?? 0,
    completion_tokens: raw.CompletionTokens ?? 0,
    cost: raw.Cost ?? 0,
    exit_code: raw.ExitCode?.Valid ? raw.ExitCode.Int64 : 0,
    error_message: raw.ErrorMessage ?? '',
    started_at: raw.StartedAt,
    completed_at: raw.EndedAt?.Valid ? raw.EndedAt.Time : null,
  }
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as unknown as T
  const text = await res.text()

  if (!res.ok) {
    let message = `HTTP ${res.status}`
    if (text.trim()) {
      if (isHtmlResponse(res, text)) {
        message = htmlRouteMessage(res)
      } else {
        try {
          const body = JSON.parse(text) as { error?: string; message?: string }
          message = body.error ?? body.message ?? message
        } catch {
          message = text.trim() || message
        }
      }
    }
    throw new ApiError(res.status, message, res.url)
  }

  if (!text.trim()) return undefined as unknown as T
  if (isHtmlResponse(res, text)) {
    throw new ApiError(res.status, htmlRouteMessage(res), res.url)
  }

  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(res.status, `API returned non-JSON for ${res.url || 'request'}.`, res.url)
  }
}

export class TorqueApiClient {
  private baseUrl: string

  constructor(baseUrl: string = '/api/v1') {
    this.baseUrl = baseUrl
  }

  private url(path: string): string {
    return `${this.baseUrl}${path}`
  }

  private async get<T>(path: string, params?: Record<string, string | number | boolean | undefined>, signal?: AbortSignal): Promise<T> {
    let url = this.url(path)
    if (params) {
      const search = new URLSearchParams()
      for (const [k, v] of Object.entries(params)) {
        if (v !== undefined) search.set(k, String(v))
      }
      const qs = search.toString()
      if (qs) url += `?${qs}`
    }
    const res = await fetch(url, { headers: { 'Accept': 'application/json' }, signal })
    return parseResponse<T>(res)
  }

  private async post<T>(path: string, body?: unknown): Promise<T> {
    const res = await fetch(this.url(path), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    return parseResponse<T>(res)
  }

  private async patch<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(this.url(path), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body),
    })
    return parseResponse<T>(res)
  }

  private async put<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(this.url(path), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body),
    })
    return parseResponse<T>(res)
  }

  private async delete<T>(path: string): Promise<T> {
    const res = await fetch(this.url(path), {
      method: 'DELETE',
      headers: { 'Accept': 'application/json' },
    })
    return parseResponse<T>(res)
  }

  // -------------------------
  // Tasks
  // -------------------------

  async listTasks(filter?: TaskFilter, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.listTaskPage(filter, signal)
  }

  /** One bounded task page; cursor mode by default, totals opt in. */
  async listTaskPage(filter?: TaskFilter, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.get<ListPage<Task>>('/tasks', taskFilterParams(filter), signal)
  }

  /** One bounded summary page, suitable for usePagedList. */
  async listTaskSummaryPage(filter?: TaskFilter, signal?: AbortSignal): Promise<ListPage<TaskSummary>> {
    return this.get<ListPage<TaskSummary>>('/tasks', { ...taskFilterParams(filter), fields: 'summary' }, signal)
  }

  /**
   * listTasks without each task's description and system_prompt
   * (`fields=summary`), for list views that render neither. The bodies were
   * most of a task list's bytes (CW-20261001-0005).
   */
  async listTaskSummaries(filter?: TaskFilter, signal?: AbortSignal): Promise<ListPage<TaskSummary>> {
    return this.listTaskSummaryPage(filter, signal)
  }

  /**
   * Per-scope task counts by status, computed server-side in one query
   * instead of paging every task to count it. `filter` narrows the cohort
   * the same way it narrows listTasks; paging fields are not accepted.
   */
  async taskRollup(groupBy: TaskScopeKey, filter?: Omit<TaskFilter, 'limit' | 'offset' | 'cursor' | 'include_total' | 'sort_by' | 'sort_dir'>): Promise<TaskScopeRollupResponse> {
    return this.get<TaskScopeRollupResponse>('/tasks/rollup', { group_by: groupBy, ...taskFilterParams(filter) })
  }

  async taskFacets(filter?: Omit<TaskFilter, 'limit' | 'offset' | 'cursor' | 'include_total' | 'sort_by' | 'sort_dir'>, dimensions = 'status', signal?: AbortSignal): Promise<TaskFacetResult> {
    return this.get<TaskFacetResult>('/tasks/facets', { ...taskFilterParams(filter), dimensions }, signal)
  }


  async getTask(id: string, signal?: AbortSignal): Promise<Task> {
    return this.get<Task>(`/tasks/${id}`, undefined, signal)
  }

  async createTask(data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task> {
    return this.post<Task>('/tasks', data)
  }

  async updateTask(id: string, data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task> {
    return this.put<Task>(`/tasks/${id}`, data)
  }

  async deleteTask(id: string): Promise<void> {
    return this.delete<void>(`/tasks/${id}`)
  }

  async transitionTask(id: string, status: string, options?: { force?: boolean }): Promise<Task> {
    const body: { status: string; force?: boolean } = { status }
    if (options?.force) body.force = true
    return this.post<Task>(`/tasks/${id}/transition`, body)
  }

  async bulkTransition(ids: string[], status: string): Promise<void> {
    return this.post<void>('/tasks/bulk-transition', { ids, status })
  }

  async searchTasks(query: string): Promise<Task[]> {
    const page = await this.get<{ items: Task[] }>('/tasks/search', { q: query })
    return page.items
  }

  // -------------------------
  // Issues
  // -------------------------

  async listIssues(filter?: ListQuery & { project_id?: string; status?: string }, signal?: AbortSignal): Promise<ListPage<Task>> {
    const { search, ...params } = filter ?? {}
    return this.get<ListPage<Task>>('/issues', { ...params, query: search }, signal)
  }

  async getIssue(id: string): Promise<Task> {
    return this.get<Task>(`/issues/${id}`)
  }

  async createIssue(data: { title: string; body?: string; context?: string; issue?: string; details?: string; project_id: string }): Promise<Task> {
    return this.post<Task>('/issues', data)
  }

  async updateIssue(id: string, data: { title?: string; body?: string; context?: string; issue?: string; details?: string; project_id?: string }): Promise<Task> {
    return this.put<Task>(`/issues/${id}`, data)
  }

  // -------------------------
  // Runs
  // -------------------------

  async pageRuns(params: RunQuery = {}, signal?: AbortSignal): Promise<RunPage> {
    const res = await this.get<{ items: ApiRunRecord[]; meta: RunPage['meta'] }>('/runs', { ...params }, signal)
    return { items: res.items.map(normalizeRun), meta: res.meta }
  }

  async runFacets(params: Pick<RunQuery, 'task_id' | 'project_id' | 'sprint_id' | 'epic_id' | 'status' | 'executor' | 'profile' | 'since' | 'until'> = {}, dimensions = 'status,executor,profile', signal?: AbortSignal): Promise<RunFacetResult> {
    return this.get<RunFacetResult>('/runs/facets', { ...params, dimensions }, signal)
  }

  async runTimeSeries(params: Pick<RunQuery, 'task_id' | 'project_id' | 'sprint_id' | 'epic_id' | 'status' | 'executor' | 'profile'> & { since: string; until: string; bucket: 'hour' | 'day'; tz_offset_minutes?: number }): Promise<RunTimeSeries> {
    return this.get<RunTimeSeries>('/runs/timeseries', { ...params })
  }

  async getRun(id: number): Promise<Run> {
    return normalizeRun(await this.get<ApiRunRecord>(`/runs/${id}`))
  }

  // -------------------------
  // Artifacts
  // -------------------------

  async listArtifacts(taskId: string, query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Artifact>> {
    const page = await this.get<ListPage<ApiArtifactRecord>>(`/tasks/${taskId}/artifacts`, { ...query }, signal)
    return { items: page.items.map(normalizeArtifact), meta: page.meta }
  }

  async createArtifact(data: {
    task_id: string
    type: string
    content?: string
    url?: string
    file_path?: string
    run_id?: number
  }): Promise<{ id: number }> {
    return this.post<{ id: number }>('/artifacts', data)
  }

  async deleteArtifact(id: number): Promise<void> {
    return this.delete<void>(`/artifacts/${id}`)
  }

  /** URL the browser can GET to stream the artifact's file content. */
  artifactContentUrl(id: number): string {
    return this.url(`/artifacts/${id}/content`)
  }

  // -------------------------
  // Comments (polymorphic — entity_type + entity_id)
  // -------------------------

  /**
   * List comments for an entity (task / collection / epic / etc).
   *
   * The HTTP layer keeps the nested /tasks/{id}/comments route alive for
   * task comments specifically; passing entity_type="task" routes through
   * that endpoint. All
   * other entity types use the flat /comments?entity_type=…&entity_id=…
   * shape.
   */
  async listComments(entityType: string, entityID: string, query?: Omit<ListQuery, 'search'>, signal?: AbortSignal): Promise<ListPage<Comment>> {
    if (entityType === 'task') {
      return this.get<ListPage<Comment>>(`/tasks/${entityID}/comments`, query, signal)
    }
    return this.get<ListPage<Comment>>('/comments', { ...query, entity_type: entityType, entity_id: entityID }, signal)
  }

  async addComment(
    entityType: string,
    entityID: string,
    content: string,
    author?: string,
  ): Promise<Comment> {
    if (entityType === 'task') {
      const body: { content: string; author?: string } = { content }
      if (author) body.author = author
      return this.post<Comment>(`/tasks/${entityID}/comments`, body)
    }
    const body: { entity_type: string; entity_id: string; content: string; author?: string } = {
      entity_type: entityType,
      entity_id: entityID,
      content,
    }
    if (author) body.author = author
    return this.post<Comment>(`/comments`, body)
  }

  // -------------------------
  // Subtodos
  // -------------------------

  async listSubtodos(taskId: string, query?: { limit?: number; cursor?: string; sort_by?: 'position'; sort_dir?: 'asc' | 'desc'; include_total?: boolean }, signal?: AbortSignal): Promise<ListPage<Subtodo>> {
    return this.get<ListPage<Subtodo>>(`/tasks/${taskId}/subtodos`, query, signal)
  }

  /**
   * Tick a single checklist item. Evidence is optional — typically an
   * artifact id, commit SHA, or URL the caller wants the item annotated
   * with. Returns the full updated list so the caller can replace local
   * state without a second GET.
   */
  async markSubtodoDone(taskId: string, itemId: string, evidence?: string): Promise<Subtodo[]> {
    const body: { evidence?: string } = {}
    if (evidence) body.evidence = evidence
    const res = await this.post<{ subtodos: Subtodo[] }>(
      `/tasks/${taskId}/subtodos/${itemId}/done`,
      body,
    )
    return res.subtodos ?? []
  }

  /**
   * Append a new subtodo. The id must be unique within the task. Returns the
   * full updated checklist.
   */
  async addSubtodo(
    taskId: string,
    item: { id: string; text: string; required?: boolean },
  ): Promise<Subtodo[]> {
    const res = await this.post<{ subtodos: Subtodo[] }>(
      `/tasks/${taskId}/subtodos`,
      item,
    )
    return res.subtodos ?? []
  }

  /**
   * Edit text and/or required flag. Omit fields to leave them unchanged.
   */
  async updateSubtodo(
    taskId: string,
    itemId: string,
    patch: { text?: string; required?: boolean },
  ): Promise<Subtodo[]> {
    const res = await this.patch<{ subtodos: Subtodo[] }>(
      `/tasks/${taskId}/subtodos/${itemId}`,
      patch,
    )
    return res.subtodos ?? []
  }

  async deleteSubtodo(taskId: string, itemId: string): Promise<Subtodo[]> {
    const res = await this.delete<{ subtodos: Subtodo[] }>(
      `/tasks/${taskId}/subtodos/${itemId}`,
    )
    return res.subtodos ?? []
  }

  // -------------------------
  // Settings
  // -------------------------

  async getSetting(key: string): Promise<string> {
    const result = await this.get<{ key: string; value: string }>(`/settings/${key}`)
    return result.value
  }

  async setSetting(key: string, value: string): Promise<void> {
    return this.put<void>(`/settings/${key}`, { value })
  }

  async getFeatureFlags(): Promise<FeatureFlags> {
    const primary = await this.get<unknown>('/settings/feature-flags').catch(() => null)
    if (
      primary &&
      typeof primary === 'object' &&
      typeof (primary as { projects?: unknown }).projects === 'boolean' &&
      typeof (primary as { epics?: unknown }).epics === 'boolean' &&
      typeof (primary as { sprints?: unknown }).sprints === 'boolean'
    ) {
      return primary as FeatureFlags
    }
    return this.get<FeatureFlags>('/features')
  }

  // -------------------------
  // Scheduler
  // -------------------------

  async getSchedulerStatus(): Promise<SchedulerStatus> {
    return this.get('/scheduler/status')
  }

  async toggleScheduler(): Promise<SchedulerStatus> {
    return this.post('/scheduler/toggle', {})
  }

  // -------------------------
  // Models (go-modelsdev catalog)
  // -------------------------

  /**
   * Returns every (provider, model) pair the catalog knows about. Cold cache
   * returns an empty list rather than failing — consumers should retry.
   * Optional `provider` narrows to a single provider id.
   */
  async listModels(provider?: string, query?: ListQuery, signal?: AbortSignal): Promise<ListPage<ModelEntry>> {
    return this.get<ListPage<ModelEntry>>('/models', { ...query, provider }, signal)
  }

  async getModel(provider: string, model: string): Promise<ModelEntry> {
    return this.get<ModelEntry>(`/models/${encodeURIComponent(provider)}/${encodeURIComponent(model)}`)
  }

  // -------------------------
  // Projects
  // -------------------------

  async listProjects(status?: string, query?: ListQuery & { include_archived?: boolean }, signal?: AbortSignal): Promise<ListPage<Project>> {
    const params: Record<string, string | number | boolean | undefined> = { ...query }
    if (status) params['status'] = status
    return this.get<ListPage<Project>>('/projects', params, signal)
  }

  async parentFacets(kind: 'project' | 'epic' | 'sprint', query: Pick<ParentQuery, 'status' | 'search' | 'project_id' | 'include_archived'>, rollupIds: string[], signal?: AbortSignal): Promise<ParentFacetResult> {
    return this.get<ParentFacetResult>(`/${kind}s/facets`, { ...query, dimensions: kind === 'epic' ? 'status,priority' : 'status', rollup_ids: rollupIds.join(',') }, signal)
  }

  async getProject(id: string): Promise<Project> {
    return this.get<Project>(`/projects/${id}`)
  }

  async createProject(data: Partial<Project>): Promise<Project> {
    return this.post<Project>('/projects', data)
  }

  async updateProject(id: string, data: Partial<Project>): Promise<Project> {
    return this.put<Project>(`/projects/${id}`, data)
  }

  async deleteProject(id: string): Promise<void> {
    return this.delete<void>(`/projects/${id}`)
  }

  async listProjectArtifacts(projectId: string): Promise<{ artifacts: ProjectArtifact[] }> {
    return this.get<{ artifacts: ProjectArtifact[] }>(`/projects/${projectId}/artifacts`)
  }

  async createProjectArtifact(
    projectId: string,
    data: Partial<ProjectArtifact> & { file_path: string }
  ): Promise<ProjectArtifact> {
    return this.post<ProjectArtifact>(`/projects/${projectId}/artifacts`, data)
  }

  async updateProjectArtifact(
    projectId: string,
    artifactId: number,
    data: Partial<ProjectArtifact>
  ): Promise<ProjectArtifact> {
    return this.put<ProjectArtifact>(`/projects/${projectId}/artifacts/${artifactId}`, data)
  }

  async deleteProjectArtifact(projectId: string, artifactId: number): Promise<void> {
    return this.delete<void>(`/projects/${projectId}/artifacts/${artifactId}`)
  }

  // -------------------------
  // Sprints
  // -------------------------

  async listSprints(params?: ListQuery & { status?: string; project_id?: string; include_archived?: boolean }, signal?: AbortSignal): Promise<ListPage<Sprint>> {
    return this.get<ListPage<Sprint>>('/sprints', { ...params }, signal)
  }

  async getSprint(id: string): Promise<Sprint> {
    return this.get<Sprint>(`/sprints/${id}`)
  }

  async createSprint(data: Partial<Sprint>): Promise<Sprint> {
    return this.post<Sprint>('/sprints', data)
  }

  async updateSprint(id: string, data: Partial<Sprint>): Promise<Sprint> {
    return this.put<Sprint>(`/sprints/${id}`, data)
  }

  async deleteSprint(id: string): Promise<void> {
    return this.delete<void>(`/sprints/${id}`)
  }

  async transitionSprint(id: string, status: string): Promise<Sprint> {
    return this.post<Sprint>(`/sprints/${id}/transition`, { status })
  }

  // -------------------------
  // Epics
  // -------------------------

  async listEpics(params?: ListQuery & { status?: string; project_id?: string; include_archived?: boolean }, signal?: AbortSignal): Promise<ListPage<Epic>> {
    return this.get<ListPage<Epic>>('/epics', { ...params }, signal)
  }

  async getEpic(id: string): Promise<Epic> {
    return this.get<Epic>(`/epics/${id}`)
  }

  async createEpic(data: Partial<Epic>): Promise<Epic> {
    return this.post<Epic>('/epics', data)
  }

  async updateEpic(id: string, data: Partial<Epic>): Promise<Epic> {
    return this.put<Epic>(`/epics/${id}`, data)
  }

  async deleteEpic(id: string): Promise<void> {
    return this.delete<void>(`/epics/${id}`)
  }

  // -------------------------
  // Tags
  // -------------------------

  async listTags(query?: { query?: string; color?: TagColor; limit?: number; cursor?: string; sort_by?: 'name'; sort_dir?: 'asc'; include_total?: boolean }, signal?: AbortSignal): Promise<ListPage<Tag>> {
    return this.get<ListPage<Tag>>('/tags', query, signal)
  }

  async getTag(slug: string): Promise<Tag> {
    return this.get<Tag>(`/tags/${slug}`)
  }

  async createTag(data: {
    name: string
    slug?: string
    description?: string
    color?: TagColor
  }): Promise<Tag> {
    return this.post<Tag>('/tags', data)
  }

  async updateTag(slug: string, data: {
    name?: string
    description?: string
    color?: TagColor
  }): Promise<Tag> {
    return this.patch<Tag>(`/tags/${slug}`, data)
  }

  async deleteTag(slug: string): Promise<void> {
    return this.delete<void>(`/tags/${slug}`)
  }

  async mergeTags(sourceSlug: string, into: string): Promise<Tag> {
    return this.post<Tag>(`/tags/${sourceSlug}/merge`, { into })
  }

  // -------------------------
  // Templates
  // -------------------------

  async listTemplates(params?: ListQuery & { kind?: string; include_archived?: boolean }, signal?: AbortSignal): Promise<ListPage<Template>> {
    return this.get<ListPage<Template>>('/templates', { ...params }, signal)
  }

  async getTemplate(id: string, version?: number): Promise<Template> {
    const qs: Record<string, string | number | boolean | undefined> = {}
    if (version !== undefined) qs['version'] = version
    return this.get<Template>(`/templates/${id}`, qs)
  }

  async createTemplate(data: Partial<Template>): Promise<Template> {
    return this.post<Template>('/templates', data)
  }

  async updateTemplate(id: string, data: Partial<Template>): Promise<Template> {
    return this.put<Template>(`/templates/${id}`, data)
  }

  async deleteTemplate(id: string): Promise<void> {
    return this.delete<void>(`/templates/${id}`)
  }

  async archiveTemplate(id: string, version: number): Promise<void> {
    return this.post<void>(`/templates/${id}/archive/${version}`)
  }

  async instantiateTemplate(id: string, body: TemplateInstantiateRequest): Promise<Task> {
    return this.post<Task>(`/templates/${id}/instantiate`, body)
  }

  // -------------------------
  // Plans
  // -------------------------

  async listPlans(query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.get<ListPage<Task>>('/plans', { ...query }, signal)
  }

  async getPlan(id: string): Promise<PlanDetail> {
    return this.get<PlanDetail>(`/plans/${id}`)
  }

  async createPlan(data: {
    title: string
    description?: string
    priority?: number
    project_id?: string
    sprint_id?: string
    epic_id?: string
    phases?: PlanPhaseInput[]
    tags?: string[]
  }): Promise<PlanDetail> {
    return this.post<PlanDetail>('/plans', data)
  }

  async addPlanPhase(planId: string, data: { name: string; acceptance?: string }): Promise<{ phase_id: string }> {
    return this.post<{ phase_id: string }>(`/plans/${planId}/phases`, data)
  }

  async removePlanPhase(planId: string, phaseId: string): Promise<void> {
    return this.delete<void>(`/plans/${planId}/phases/${phaseId}`)
  }

  async listPlanChildren(planId: string, phaseId?: string, query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.get<ListPage<Task>>(`/plans/${planId}/children`, { ...query, phase_id: phaseId }, signal)
  }

  /**
   * Boot an Orchestrator session for the named plan and transition the
   * plan task to `doing`. Returns `{session_id, plan_id, started_at}`.
   * On 409 (already orchestrating) the existing session_id is surfaced
   * so callers can route to the live session view rather than retry.
   * CW-20260503-0017 (S2.1).
   */
  async startPlan(planId: string, opts?: { workdir?: string; env?: string[] }): Promise<{
    session_id: string
    plan_id: string
    started_at: string
  }> {
    return this.post(`/plans/${planId}/start`, opts ?? {})
  }

  // -------------------------
  // Collections
  // -------------------------

  async listCollections(status?: 'active' | 'archived' | 'all', query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Collection>> {
    return this.get<ListPage<Collection>>('/collections', { ...query, status }, signal)
  }

  async getCollection(id: string): Promise<Collection> {
    return this.get<Collection>(`/collections/${id}`)
  }

  async createCollection(name: string, description?: string): Promise<Collection> {
    return this.post<Collection>('/collections', {
      name,
      description: description ?? '',
    })
  }

  async updateCollection(
    id: string,
    fields: { name?: string; description?: string },
  ): Promise<Collection> {
    return this.put<Collection>(`/collections/${id}`, fields)
  }

  async archiveCollection(id: string): Promise<Collection> {
    return this.post<Collection>(`/collections/${id}/archive`)
  }

  async unarchiveCollection(id: string): Promise<Collection> {
    return this.post<Collection>(`/collections/${id}/unarchive`)
  }

  async listCollectionTasks(collectionId: string, query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.get<ListPage<Task>>(`/collections/${collectionId}/tasks`, { ...query }, signal)
  }

  async addTaskToCollection(
    collectionId: string,
    taskId: string,
    position?: number,
  ): Promise<void> {
    const body: { task_id: string; position?: number } = { task_id: taskId }
    if (position !== undefined) body.position = position
    return this.post<void>(`/collections/${collectionId}/tasks`, body)
  }

  async removeTaskFromCollection(collectionId: string, taskId: string): Promise<void> {
    return this.delete<void>(`/collections/${collectionId}/tasks/${taskId}`)
  }

  async reorderCollectionTasks(collectionId: string, taskIds: string[]): Promise<void> {
    return this.put<void>(`/collections/${collectionId}/tasks/order`, {
      task_ids: taskIds,
    })
  }

  /**
   * Cross-collection move. `targetCollectionId` is required by the
   * backend; pass an empty string to clear membership (returns the task
   * to the inbox) — but prefer `addTaskToInbox` for clarity, which the
   * backend treats as the canonical inbox path.
   */
  async moveTask(
    taskId: string,
    targetCollectionId: string,
    position?: number,
  ): Promise<void> {
    const body: { task_id: string; target_collection_id: string; position?: number } = {
      task_id: taskId,
      target_collection_id: targetCollectionId,
    }
    if (position !== undefined) body.position = position
    return this.post<void>('/collections/tasks/move', body)
  }

  async listInboxTasks(query?: ListQuery, signal?: AbortSignal): Promise<ListPage<Task>> {
    return this.get<ListPage<Task>>('/collections/inbox/tasks', { ...query }, signal)
  }

  async addTaskToInbox(taskId: string): Promise<void> {
    return this.post<void>('/collections/inbox/tasks', { task_id: taskId })
  }

  // -------------------------
  // Checkpoints
  // -------------------------

  async listPendingCheckpoints(query?: ListQuery): Promise<ListPage<Checkpoint>> {
    return this.get<ListPage<Checkpoint>>('/checkpoints/pending', { ...query })
  }

  async listTaskCheckpoints(taskId: string, query?: ListQuery): Promise<ListPage<Checkpoint>> {
    return this.get<ListPage<Checkpoint>>(`/tasks/${taskId}/checkpoints`, { ...query })
  }

  async getCheckpoint(correlationId: string): Promise<Checkpoint> {
    return this.get<Checkpoint>(`/checkpoints/${correlationId}`)
  }

  async emitCheckpoint(body: CheckpointEmitRequest): Promise<Checkpoint> {
    return this.post<Checkpoint>('/checkpoints', body)
  }

  async listHITLWorkflowDefinitions(): Promise<HITLWorkflowListResponse> {
    return this.get<HITLWorkflowListResponse>('/checkpoint-workflows')
  }

  async getHITLWorkflowDefinition(type: string): Promise<HITLWorkflowDefinition> {
    return this.get<HITLWorkflowDefinition>(`/checkpoint-workflows/${encodeURIComponent(type)}`)
  }

  async respondCheckpoint(
    correlationId: string,
    body: { response_json: string; responder_source_type: string; responder_source_ref?: string }
  ): Promise<Checkpoint> {
    return this.post<Checkpoint>(`/checkpoints/${correlationId}/respond`, body)
  }

  async cancelCheckpoint(
    correlationId: string,
    body: { reason?: string; canceler_source_type?: string; canceler_source_ref?: string }
  ): Promise<Checkpoint> {
    return this.post<Checkpoint>(`/checkpoints/${correlationId}/cancel`, body)
  }

  // -------------------------
  // Admin
  // -------------------------

  async restartFrontend(): Promise<{ hash: string; duration_ms: number }> {
    return this.post<{ hash: string; duration_ms: number }>('/admin/restart-frontend')
  }

  // -------------------------
  // Messaging
  // -------------------------

  /**
   * Drain the inbox for a recipient URN via `GET /messages/inbox`.
   *
   * NOTE: this is destructive in the go-messaging sense — the backend marks
   * every returned envelope `delivered` for `to`, so a subsequent call
   * returns only envelopes that arrived since. The messaging page therefore
   * MERGES results into accumulated state rather than replacing them, and
   * the action is operator-initiated (not auto-polled) so it does not
   * silently consume delivery on an agent's behalf.
   */
  async getInbox(to: string, filter?: MessageFilter & { include_total?: boolean }): Promise<ListPage<MessageEnvelope>> {
    return this.get<ListPage<MessageEnvelope>>('/messages/inbox', { ...filter, to })
  }

  /** Read a thread by id — non-destructive (`GET /messages/thread/{id}`). */
  async getThread(threadId: string, filter?: MessageFilter & ListQuery, signal?: AbortSignal): Promise<ListPage<MessageEnvelope>> {
    return this.get<ListPage<MessageEnvelope>>(`/messages/thread/${encodeURIComponent(threadId)}`, { ...filter }, signal)
  }

  /** Fetch a single envelope by id — non-destructive. */
  async getMessage(id: string): Promise<MessageEnvelope> {
    return this.get<MessageEnvelope>(`/messages/${encodeURIComponent(id)}`)
  }

  /** Send an envelope through the messaging store (`POST /messages`). */
  async sendMessage(req: SendMessageRequest): Promise<MessageEnvelope> {
    return this.post<MessageEnvelope>('/messages', req)
  }

  /** Retract an undelivered message (`POST /messages/{id}/cancel`). */
  async cancelMessage(id: string): Promise<void> {
    await this.post<void>(`/messages/${encodeURIComponent(id)}/cancel`)
  }

  /** Mark a message consumed for a recipient (`POST /messages/{id}/consume`). */
  async consumeMessage(id: string, recipient: string): Promise<void> {
    await this.post<void>(`/messages/${encodeURIComponent(id)}/consume`, { recipient })
  }

  /**
   * Send a typed envelope through the broker (`POST /broker/send`) — adds
   * validation, a payload size cap, and SSE publication on top of the store.
   */
  async brokerSend(req: SendMessageRequest): Promise<MessageEnvelope> {
    return this.post<MessageEnvelope>('/broker/send', req)
  }

  /**
   * Send a `request` envelope and block until the addressed peer answers
   * (`POST /broker/request`). Resolves with the response envelope; rejects
   * with a 504 if the peer stays silent past `timeout_seconds`.
   */
  async brokerRequest(req: BrokerRequest): Promise<MessageEnvelope> {
    return this.post<MessageEnvelope>('/broker/request', req)
  }

  /**
   * Drain the broker inbox for a recipient URN (`GET /broker/inbox`).
   *
   * Destructive like getInbox — the broker marks every returned envelope
   * `delivered` for `to` and publishes an `envelope.delivered` SSE frame,
   * so callers should MERGE results into accumulated state, not replace.
   */
  async brokerInbox(to: string, filter?: MessageFilter): Promise<MessageEnvelope[]> {
    const res = await this.get<{ envelopes: MessageEnvelope[] | null }>('/broker/inbox', {
      to,
      kind: filter?.kind,
      channel: filter?.channel,
      thread_id: filter?.thread_id,
      limit: filter?.limit,
    })
    return res.envelopes ?? []
  }

  /**
   * Open a live SSE stream of envelopes addressed to `to`
   * (`GET /messages/subscribe`). The backend emits `event: message` frames;
   * returns an unsubscribe fn. Reconnects with a fixed backoff on error.
   */
  subscribeMessages(
    to: string,
    onMessage: (env: MessageEnvelope) => void,
    onStatus?: (status: 'connecting' | 'live' | 'error') => void,
  ): () => void {
    const url = this.url(`/messages/subscribe?to=${encodeURIComponent(to)}`)
    let es: EventSource | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let stopped = false

    function connect() {
      onStatus?.('connecting')
      es = new EventSource(url)
      es.onopen = () => onStatus?.('live')
      es.onmessage = (e: MessageEvent) => {
        try {
          onMessage(JSON.parse(e.data as string) as MessageEnvelope)
        } catch {
          // malformed frame — ignore
        }
      }
      es.onerror = () => {
        es?.close()
        if (!stopped) {
          onStatus?.('error')
          reconnectTimer = setTimeout(connect, 3000)
        }
      }
    }

    connect()

    return () => {
      stopped = true
      if (reconnectTimer !== null) clearTimeout(reconnectTimer)
      es?.close()
    }
  }

  // -------------------------
  // SSE
  // -------------------------

  subscribeEvents(
    onEvent: (event: SSEEvent) => void,
    onStatus?: (status: 'connecting' | 'live' | 'error') => void,
  ): () => void {
    const url = this.url('/events')
    let es: EventSource | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let stopped = false

    function connect() {
      onStatus?.('connecting')
      es = new EventSource(url)

      es.onopen = () => {
        onStatus?.('live')
      }

      es.onmessage = (e: MessageEvent) => {
        try {
          const parsed = JSON.parse(e.data as string) as SSEEvent
          onEvent(parsed)
        } catch {
          // malformed event — ignore
        }
      }

      es.onerror = () => {
        es?.close()
        if (!stopped) {
          onStatus?.('error')
          reconnectTimer = setTimeout(connect, 3000)
        }
      }
    }

    connect()

    return () => {
      stopped = true
      if (reconnectTimer !== null) clearTimeout(reconnectTimer)
      es?.close()
    }
  }
}
