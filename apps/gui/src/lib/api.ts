import type {
  Task,
  TaskFilter,
  Run,
  Artifact,
  Comment,
  SSEEvent,
  FeatureFlags,
} from './types'

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
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

  async createTask(data: Partial<Task>): Promise<Task> {
    return this.post<Task>('/tasks', data)
  }

  async updateTask(id: string, data: Partial<Task>): Promise<Task> {
    return this.patch<Task>(`/tasks/${id}`, data)
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
    return this.get<Run[]>(`/tasks/${taskId}/runs`)
  }

  async getRun(id: number): Promise<Run> {
    return this.get<Run>(`/runs/${id}`)
  }

  // -------------------------
  // Artifacts
  // -------------------------

  async listArtifacts(taskId: string): Promise<Artifact[]> {
    return this.get<Artifact[]>(`/tasks/${taskId}/artifacts`)
  }

  async createArtifact(taskId: string, data: Partial<Artifact>): Promise<Artifact> {
    return this.post<Artifact>(`/tasks/${taskId}/artifacts`, data)
  }

  // -------------------------
  // Comments
  // -------------------------

  async listComments(taskId: string): Promise<Comment[]> {
    return this.get<Comment[]>(`/tasks/${taskId}/comments`)
  }

  async addComment(taskId: string, content: string): Promise<Comment> {
    return this.post<Comment>(`/tasks/${taskId}/comments`, { content })
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
