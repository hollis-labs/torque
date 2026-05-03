import type {
  Task,
  TaskFilter,
  Run,
  Artifact,
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
  Subtodo,
  PlanDetail,
  PlanPhaseInput,
  SchedulerStatus,
  ModelEntry,
} from './types'

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

/**
 * Coerce any of the shapes the artifact list endpoints have historically
 * returned into a real `Artifact[]`. The frontend shipped against two
 * variants: a `{artifacts: [...]}` envelope (both legacy and alias
 * routes) and, briefly during overnight merges, a bare array. We also
 * defensively treat null / unexpected bodies as an empty list so the
 * tab renders its empty state instead of crashing on `.map`.
 */
export function normalizeArtifactList(raw: unknown): Artifact[] {
  if (Array.isArray(raw)) {
    return (raw as ApiArtifactRecord[]).map(normalizeArtifact)
  }
  if (raw && typeof raw === 'object') {
    const inner = (raw as { artifacts?: unknown }).artifacts
    if (Array.isArray(inner)) {
      return (inner as ApiArtifactRecord[]).map(normalizeArtifact)
    }
  }
  return []
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

export class ClockworkApiClient {
  private baseUrl: string

  constructor(baseUrl: string = '/api/v1') {
    this.baseUrl = baseUrl
  }

  private url(path: string): string {
    return `${this.baseUrl}${path}`
  }

  private async get<T>(path: string, params?: Record<string, string | number | boolean | undefined>): Promise<T> {
    let url = this.url(path)
    if (params) {
      const search = new URLSearchParams()
      for (const [k, v] of Object.entries(params)) {
        if (v !== undefined) search.set(k, String(v))
      }
      const qs = search.toString()
      if (qs) url += `?${qs}`
    }
    const res = await fetch(url, { headers: { 'Accept': 'application/json' } })
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

  async listTasks(filter?: TaskFilter): Promise<{ tasks: Task[]; total: number }> {
    const params: Record<string, string | number | boolean | undefined> = {}
    if (filter?.status?.length) params['status'] = filter.status.join(',')
    if (filter?.priority?.length) params['priority'] = filter.priority.join(',')
    if (filter?.tags?.length) params['tags'] = filter.tags.join(',')
    if (filter?.sprint_id) params['sprint_id'] = filter.sprint_id
    if (filter?.project_id) params['project_id'] = filter.project_id
    if (filter?.epic_id) params['epic_id'] = filter.epic_id
    if (filter?.search) params['search'] = filter.search
    if (filter?.limit !== undefined) params['limit'] = filter.limit
    if (filter?.offset !== undefined) params['offset'] = filter.offset
    if (filter?.kind) params['kind'] = filter.kind
    if (filter?.parent_id !== undefined) params['parent_id'] = filter.parent_id
    if (filter?.manual !== undefined) params['manual'] = filter.manual ? 'true' : 'false'
    return this.get<{ tasks: Task[]; total: number }>('/tasks', params)
  }

  async getTask(id: string): Promise<Task> {
    return this.get<Task>(`/tasks/${id}`)
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
    return this.get<Task[]>('/tasks/search', { q: query })
  }

  // -------------------------
  // Runs
  // -------------------------

  async listRuns(taskId: string): Promise<Run[]> {
    const res = await this.get<{ runs: ApiRunRecord[] }>('/runs', { task_id: taskId })
    return (res.runs ?? []).map(normalizeRun)
  }

  /**
   * Aggregate feed across all tasks, newest-first. Clamped server-side to
   * [1, 1000]; default 200. Powers the Ops dashboard widgets which need a
   * single cross-task window instead of a per-task fan-out.
   */
  async listAllRuns(params?: {
    limit?: number
    since?: string
    status?: string
    project_id?: string
  }): Promise<Run[]> {
    const qs: Record<string, string | number | boolean | undefined> = {}
    if (params?.limit !== undefined) qs['limit'] = params.limit
    if (params?.since) qs['since'] = params.since
    if (params?.status) qs['status'] = params.status
    if (params?.project_id) qs['project_id'] = params.project_id
    const res = await this.get<{ runs: ApiRunRecord[] }>('/runs', qs)
    return (res.runs ?? []).map(normalizeRun)
  }

  async getRun(id: number): Promise<Run> {
    return normalizeRun(await this.get<ApiRunRecord>(`/runs/${id}`))
  }

  // -------------------------
  // Artifacts
  // -------------------------

  async listArtifacts(taskId: string): Promise<Artifact[]> {
    // Try the nested route first, fall back to the legacy query-string
    // form. Both are served by the same backend handler and return a
    // `{artifacts: [...]}` envelope, but older bundled server binaries
    // predate the alias and fall through to the SPA index.html for the
    // nested URL — that makes `res.json()` reject, so we retry against
    // the query-string form. On 404 / transport failure / unrecognized
    // body we return `[]` so the UI renders an empty state instead of
    // crashing on a non-array `.map`.
    const attempts: Array<() => Promise<unknown>> = [
      () => this.get<unknown>(`/tasks/${taskId}/artifacts`),
      () => this.get<unknown>('/artifacts', { task_id: taskId }),
    ]
    for (const attempt of attempts) {
      try {
        return normalizeArtifactList(await attempt())
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return []
        // Any other failure (non-JSON body, 5xx, network) falls through
        // to the next attempt; the final catch returns [].
      }
    }
    return []
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
   * that endpoint so existing nested tests / clients keep working. All
   * other entity types use the flat /comments?entity_type=…&entity_id=…
   * shape.
   */
  async listComments(entityType: string, entityID: string): Promise<Comment[]> {
    if (entityType === 'task') {
      return this.get<Comment[]>(`/tasks/${entityID}/comments`)
    }
    const qs = new URLSearchParams({ entity_type: entityType, entity_id: entityID })
    return this.get<Comment[]>(`/comments?${qs.toString()}`)
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

  async listSubtodos(taskId: string): Promise<Subtodo[]> {
    const res = await this.get<{ subtodos: Subtodo[] }>(`/tasks/${taskId}/subtodos`)
    return res.subtodos ?? []
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
  async listModels(provider?: string): Promise<ModelEntry[]> {
    const params: Record<string, string | number | boolean | undefined> = {}
    if (provider) params['provider'] = provider
    const res = await this.get<{ models: ModelEntry[] }>('/models', params)
    return res.models ?? []
  }

  async getModel(provider: string, model: string): Promise<ModelEntry> {
    return this.get<ModelEntry>(`/models/${encodeURIComponent(provider)}/${encodeURIComponent(model)}`)
  }

  // -------------------------
  // Projects
  // -------------------------

  async listProjects(status?: string): Promise<{ projects: Project[] }> {
    const params: Record<string, string | number | boolean | undefined> = {}
    if (status) params['status'] = status
    return this.get<{ projects: Project[] }>('/projects', params)
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

  async listSprints(params?: { status?: string; project_id?: string }): Promise<{ sprints: Sprint[] }> {
    return this.get<{ sprints: Sprint[] }>('/sprints', params)
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

  async listEpics(params?: { status?: string; project_id?: string }): Promise<{ epics: Epic[] }> {
    return this.get<{ epics: Epic[] }>('/epics', params)
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

  async listTags(): Promise<{ tags: Tag[] }> {
    return this.get<{ tags: Tag[] }>('/tags')
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

  async listTemplates(params?: { kind?: string; include_archived?: boolean }): Promise<{ templates: Template[] }> {
    const qs: Record<string, string | number | boolean | undefined> = {}
    if (params?.kind) qs['kind'] = params.kind
    if (params?.include_archived) qs['include_archived'] = 'true'
    return this.get<{ templates: Template[] }>('/templates', qs)
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

  async listPlans(): Promise<{ plans: Task[] }> {
    return this.get<{ plans: Task[] }>('/plans')
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

  async listPlanChildren(planId: string, phaseId?: string): Promise<{ tasks: Task[] }> {
    const params: Record<string, string | number | boolean | undefined> = {}
    if (phaseId) params['phase_id'] = phaseId
    return this.get<{ tasks: Task[] }>(`/plans/${planId}/children`, params)
  }

  // -------------------------
  // Checkpoints
  // -------------------------

  async listPendingCheckpoints(): Promise<{ checkpoints: Checkpoint[] }> {
    return this.get<{ checkpoints: Checkpoint[] }>('/checkpoints/pending')
  }

  async listTaskCheckpoints(taskId: string): Promise<{ checkpoints: Checkpoint[] }> {
    return this.get<{ checkpoints: Checkpoint[] }>(`/tasks/${taskId}/checkpoints`)
  }

  async getCheckpoint(correlationId: string): Promise<Checkpoint> {
    return this.get<Checkpoint>(`/checkpoints/${correlationId}`)
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
