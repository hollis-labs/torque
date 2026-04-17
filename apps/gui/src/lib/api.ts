import type {
  Task,
  TaskFilter,
  Run,
  Artifact,
  Comment,
  SSEEvent,
  FeatureFlags,
  Project,
  Sprint,
  Epic,
  Tag,
  TagColor,
} from './types'

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
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
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const body = await res.json() as { error?: string; message?: string }
      message = body.error ?? body.message ?? message
    } catch {
      // ignore parse errors
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as unknown as T
  return res.json() as Promise<T>
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

  async transitionTask(id: string, status: string): Promise<Task> {
    return this.post<Task>(`/tasks/${id}/transition`, { status })
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

  async getRun(id: number): Promise<Run> {
    return normalizeRun(await this.get<ApiRunRecord>(`/runs/${id}`))
  }

  // -------------------------
  // Artifacts
  // -------------------------

  async listArtifacts(taskId: string): Promise<Artifact[]> {
    const res = await this.get<{ artifacts: ApiArtifactRecord[] }>(`/tasks/${taskId}/artifacts`)
    return (res.artifacts ?? []).map(normalizeArtifact)
  }

  /** URL the browser can GET to stream the artifact's file content. */
  artifactContentUrl(id: number): string {
    return this.url(`/artifacts/${id}/content`)
  }

  // -------------------------
  // Comments
  // -------------------------

  async listComments(taskId: string): Promise<Comment[]> {
    return this.get<Comment[]>(`/tasks/${taskId}/comments`)
  }

  async addComment(taskId: string, content: string, author?: string): Promise<Comment> {
    const body: { content: string; author?: string } = { content }
    if (author) body.author = author
    return this.post<Comment>(`/tasks/${taskId}/comments`, body)
  }

  // -------------------------
  // Settings
  // -------------------------

  async getSetting(key: string): Promise<string> {
    const result = await this.get<{ key: string; value: string }>(`/settings/${key}`)
    return result.value
  }

  async setSetting(key: string, value: string): Promise<void> {
    return this.post<void>(`/settings/${key}`, { value })
  }

  async getFeatureFlags(): Promise<FeatureFlags> {
    return this.get<FeatureFlags>('/settings/feature-flags')
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
  // SSE
  // -------------------------

  subscribeEvents(onEvent: (event: SSEEvent) => void): () => void {
    const url = this.url('/events')
    let es: EventSource | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let stopped = false

    function connect() {
      es = new EventSource(url)

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
