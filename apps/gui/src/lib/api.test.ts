import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { TorqueApiClient } from './api'

function jsonResponse(body: unknown, init: Partial<ResponseInit> = {}): Response {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', ...(init.headers ?? {}) },
  })
}

function htmlResponse(body = '<!doctype html><html></html>', status = 200): Response {
  return new Response(body, {
    status,
    headers: { 'Content-Type': 'text/html' },
  })
}

const ENVELOPE_RECORD = {
  ID: 1,
  TaskID: 'TASK-1',
  RunID: { Int64: 0, Valid: false },
  Type: 'log',
  Content: 'hello',
  URL: '',
  FilePath: '',
  Metadata: { String: '', Valid: false },
  CreatedAt: '2026-04-17T00:00:00Z',
}

describe('TorqueApiClient.listArtifacts', () => {
  const client = new TorqueApiClient('/api/v1')
  afterEach(() => vi.restoreAllMocks())
  it('normalizes one envelope page and preserves continuation metadata', async () => {
    const meta = { returned: 1, limit: 50, has_more: true, next_cursor: 'next', total: 205 }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [ENVELOPE_RECORD], meta }))
    globalThis.fetch = fetchMock as unknown as typeof fetch
    const out = await client.listArtifacts('TASK-1')
    expect(out.items).toHaveLength(1)
    expect(out.items[0].id).toBe(1)
    expect(out.items[0].task_id).toBe('TASK-1')
    expect(out.meta).toEqual(meta)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/tasks/TASK-1/artifacts')
  })
  it('surfaces errors after one request without retrying legacy shapes', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('NetworkError'))
    globalThis.fetch = fetchMock as unknown as typeof fetch
    await expect(client.listArtifacts('TASK-1')).rejects.toThrow('NetworkError')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('TorqueApiClient.listTasks', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  function calledUrls(): string[] {
    return fetchMock.mock.calls.map((c) => String(c[0]))
  }

  it('returns exactly one page, leaving continuation and optional totals to the caller', async () => {
    const page = { items: [{ id: 'TASK-1' }, { id: 'TASK-2' }], meta: {
      returned: 2, limit: 2, has_more: true, next_cursor: 'next',
    } }
    fetchMock.mockResolvedValueOnce(jsonResponse(page))
    const out = await client.listTasks({ status: ['todo'], tags: ['torque', 'api'] })
    expect(out).toEqual(page)
    expect(calledUrls()).toEqual(['/api/v1/tasks?status=todo&tags=torque%2Capi'])
  })

  it('preserves an explicit offset and requested total without collecting subsequent pages', async () => {
    const page = { items: [{ id: 'TASK-1' }], meta: {
      total: 400, returned: 1, limit: 50, offset: 10, has_more: true, next_cursor: null, next_offset: 60,
    } }
    fetchMock.mockResolvedValueOnce(jsonResponse(page))
    expect(await client.listTasks({ limit: 50, offset: 10, include_total: true, manual: true })).toEqual(page)
    expect(calledUrls()).toEqual(['/api/v1/tasks?limit=50&offset=10&include_total=true&manual=true'])
  })

  it('listTaskSummaries returns one summary page without forcing totals or offsets', async () => {
    const page = { items: [{ id: 'TASK-1' }], meta: { returned: 1, limit: 50, has_more: true, next_cursor: 'next' } }
    fetchMock.mockResolvedValueOnce(jsonResponse(page))
    expect(await client.listTaskSummaries({ project_id: 'PRJ-1' })).toEqual(page)
    expect(calledUrls()).toEqual(['/api/v1/tasks?project_id=PRJ-1&fields=summary'])
  })
})

describe('TorqueApiClient.taskRollup', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('makes one GET to /tasks/rollup with group_by and the filter', async () => {
    const body = { group_by: 'epic_id', total: 3, scopes: [{ scope_id: 'EP-1', total: 3, counts: { todo: 2, done: 1 } }] }
    fetchMock.mockResolvedValueOnce(jsonResponse(body))

    await expect(client.taskRollup('epic_id', { project_id: 'PRJ-1' })).resolves.toEqual(body)
    expect(fetchMock.mock.calls.map((c) => String(c[0]))).toEqual([
      '/api/v1/tasks/rollup?group_by=epic_id&project_id=PRJ-1',
    ])
  })
})

describe('TorqueApiClient.setSetting', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('uses PUT against the keyed settings route', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ key: 'features.projects', value: 'true' }))

    await client.setSetting('features.projects', 'true')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/settings/features.projects', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify({ value: 'true' }),
    })
  })
})

describe('TorqueApiClient.emitCheckpoint', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('posts typed HITL checkpoint emits to /checkpoints', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({
      id: 1,
      task_id: 'TASK-1',
      run_id: null,
      correlation_id: 'corr-1',
      type: 'approval',
      payload_json: '{}',
      response_json: null,
      emitter_source_type: 'user',
      emitter_source_ref: 'gui',
      responder_source_type: null,
      responder_source_ref: null,
      emitted_at: '2026-05-11T00:00:00Z',
      responded_at: null,
      timeout_at: null,
      status: 'pending',
    }))

    await client.emitCheckpoint({
      task_id: 'TASK-1',
      type: 'approval',
      payload_json: '{"title":"Approval"}',
      emitter_source_type: 'user',
      emitter_source_ref: 'gui',
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/checkpoints', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify({
        task_id: 'TASK-1',
        type: 'approval',
        payload_json: '{"title":"Approval"}',
        emitter_source_type: 'user',
        emitter_source_ref: 'gui',
      }),
    })
  })
})

describe('TorqueApiClient.getFeatureFlags', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('accepts the modern /settings/feature-flags response shape', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ projects: true, epics: false, sprints: true }))
    await expect(client.getFeatureFlags()).resolves.toEqual({ projects: true, epics: false, sprints: true })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('falls back to /features when the old backend returns a keyed setting record', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ key: 'feature-flags', value: '' }))
      .mockResolvedValueOnce(jsonResponse({ projects: true, epics: true, sprints: false }))
    await expect(client.getFeatureFlags()).resolves.toEqual({ projects: true, epics: true, sprints: false })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/settings/feature-flags')
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/v1/features')
  })
})

describe('TorqueApiClient HTML fallback errors', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('throws a route-focused error when a JSON endpoint returns HTML with 200', async () => {
    fetchMock.mockResolvedValueOnce(htmlResponse())
    const promise = client.getProject('PR-1')
    await expect(promise).rejects.toMatchObject({
      name: 'ApiError',
      status: 200,
    })
    await expect(promise).rejects.toThrow(/returned HTML instead of JSON/i)
  })
})

describe('TorqueApiClient messaging client', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>

  const ENVELOPE = {
    id: 'msg-1',
    kind: 'notice',
    from: 'msg://user/local/operator',
    to: 'msg://agent/local/orchestrator',
    created_at: '2026-05-18T00:00:00Z',
    delivered_at: null,
    consumed_at: null,
  }

  beforeEach(() => {
    fetchMock = vi.fn()
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('getInbox drains exactly one items/meta page', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ items: [ENVELOPE], meta: { returned: 1, limit: 50, has_more: false, next_cursor: null } }))
    const out = await client.getInbox('msg://user/local/operator')
    expect(out.items).toHaveLength(1)
    expect(out.items[0].id).toBe('msg-1')
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/v1/messages/inbox?to=msg%3A%2F%2Fuser%2Flocal%2Foperator',
    )
  })

  it('getInbox preserves the empty page metadata', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null } }))
    expect((await client.getInbox('msg://user/local/operator')).items).toEqual([])
  })

  it('getThread passes filter params to GET /messages/thread/{id}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ items: [ENVELOPE], meta: { returned: 1, limit: 50, has_more: false, next_cursor: null } }))
    await client.getThread('thread-1', { kind: 'response', limit: 10 })
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/v1/messages/thread/thread-1?kind=response&limit=10',
    )
  })

  it('sendMessage POSTs the envelope to /messages', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(ENVELOPE, { status: 201 }))
    await client.sendMessage({
      kind: 'notice',
      from: 'msg://user/local/operator',
      to: 'msg://agent/local/orchestrator',
      payload: { body: 'hi' },
    })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/messages')
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' })
  })

  it('cancelMessage POSTs to /messages/{id}/cancel', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({}))
    await client.cancelMessage('msg-1')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/messages/msg-1/cancel')
  })

  it('consumeMessage POSTs the recipient to /messages/{id}/consume', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({}))
    await client.consumeMessage('msg-1', 'msg://agent/local/orchestrator')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/messages/msg-1/consume')
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ recipient: 'msg://agent/local/orchestrator' }),
    })
  })

  it('brokerSend POSTs the envelope to /broker/send', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(ENVELOPE, { status: 201 }))
    await client.brokerSend({
      kind: 'notice',
      from: 'msg://user/local/operator',
      to: 'msg://agent/local/orchestrator',
    })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/broker/send')
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' })
  })

  it('brokerRequest POSTs a request body to /broker/request', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(ENVELOPE))
    const out = await client.brokerRequest({
      from: 'msg://user/local/operator',
      to: 'msg://agent/local/orchestrator',
      payload: { q: 'status?' },
      timeout_seconds: 15,
    })
    expect(out.id).toBe('msg-1')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/broker/request')
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({
        from: 'msg://user/local/operator',
        to: 'msg://agent/local/orchestrator',
        payload: { q: 'status?' },
        timeout_seconds: 15,
      }),
    })
  })

  it('brokerInbox drains GET /broker/inbox and unwraps {envelopes}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ envelopes: [ENVELOPE] }))
    const out = await client.brokerInbox('msg://agent/local/orchestrator', { limit: 5 })
    expect(out).toHaveLength(1)
    expect(out[0].id).toBe('msg-1')
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/v1/broker/inbox?to=msg%3A%2F%2Fagent%2Flocal%2Forchestrator&limit=5',
    )
  })

  it('brokerInbox returns [] when the envelope key is null', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ envelopes: null }))
    await expect(client.brokerInbox('msg://agent/local/orchestrator')).resolves.toEqual([])
  })
})

describe('adjacent list pages', () => {
  const client = new TorqueApiClient('/api/v1')
  let fetchMock: ReturnType<typeof vi.fn>
  const page = {
    items: [{ id: 'row-1' }],
    meta: { returned: 1, limit: 50, has_more: true, next_cursor: 'next-page', total: 205 },
  }

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue(jsonResponse(page))
    globalThis.fetch = fetchMock as unknown as typeof fetch
  })

  it.each([
    ['projects', () => client.listProjects('active', { search: 'needle', include_total: true }), '/projects', 'search'],
    ['epics', () => client.listEpics({ project_id: 'PRJ-1', search: 'needle', include_total: true }), '/epics', 'search'],
    ['sprints', () => client.listSprints({ project_id: 'PRJ-1', search: 'needle', include_total: true }), '/sprints', 'search'],
    ['issues', () => client.listIssues({ project_id: 'PRJ-1', search: 'needle', include_total: true }), '/issues', 'query'],
  ] as const)('returns one %s page without traversing continuation', async (_name, list, path, queryKey) => {
    expect(await list()).toEqual(page)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const url = new URL(fetchMock.mock.calls[0][0] as string, 'http://localhost')
    expect(url.pathname).toBe(`/api/v1${path}`)
    expect(url.searchParams.get(queryKey)).toBe('needle')
    expect(url.searchParams.get('include_total')).toBe('true')
  })

  it.each([
    ['task', '/api/v1/tasks/ROW-1/comments'],
    ['epic', '/api/v1/comments'],
  ])('returns one %s comment page and forwards cursor', async (entityType, path) => {
    expect(await client.listComments(entityType, 'ROW-1', { cursor: 'previous', limit: 2 })).toEqual(page)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const url = new URL(fetchMock.mock.calls[0][0] as string, 'http://localhost')
    expect(url.pathname).toBe(path)
    expect(url.searchParams.get('cursor')).toBe('previous')
    expect(url.searchParams.get('limit')).toBe('2')
    if (entityType !== 'task') {
      expect(url.searchParams.get('entity_type')).toBe(entityType)
      expect(url.searchParams.get('entity_id')).toBe('ROW-1')
    }
  })
})

describe('TorqueApiClient.pageRuns', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('normalizes rows and preserves cursor metadata and server filters', async () => {
    const meta = { returned: 1, limit: 50, has_more: true, next_cursor: 'opaque-cursor', total: 80 }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      items: [{ ID: 9, TaskID: 'T-9', Status: 'done', Executor: 'mock', StartedAt: '2026-10-01T00:00:00Z', Cost: 1.25 }], meta,
    }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new TorqueApiClient('/api/v1')
    const page = await client.pageRuns({ status: 'done,failed', sort_by: 'cost', sort_dir: 'desc', cursor: 'previous', include_total: true })
    expect(page.items[0]).toMatchObject({ id: 9, task_id: 'T-9', status: 'done', cost: 1.25 })
    expect(page.meta).toEqual(meta)
    const params = new URL(fetchMock.mock.calls[0][0], 'http://localhost').searchParams
    expect(params.get('status')).toBe('done,failed')
    expect(params.get('cursor')).toBe('previous')
    expect(params.get('include_total')).toBe('true')
    expect(params.has('limit')).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('returns an empty final page without synthesizing a count or prefetching', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null } }))
    vi.stubGlobal('fetch', fetchMock)
    const page = await new TorqueApiClient('/api/v1').pageRuns({ task_id: 'T-1' })
    expect(page.items).toEqual([])
    expect(page.meta.total).toBeUndefined()
    expect(page.meta.next_cursor).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('single-page task adapters', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches one summary cursor page, preserves optional metadata, and forwards cancellation', async () => {
    const page = { items: [{ id: 'T-1' }], meta: { returned: 1, limit: 2, has_more: true, next_cursor: 'next' } }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page))
    vi.stubGlobal('fetch', fetchMock)
    const abort = new AbortController()
    const result = await new TorqueApiClient('/api/v1').listTaskSummaryPage({ project_id: 'PRJ-1', limit: 2, cursor: 'previous' }, abort.signal)
    expect(result).toEqual(page)
    expect(fetchMock).toHaveBeenCalledOnce()
    const url = new URL(fetchMock.mock.calls[0][0], 'http://localhost')
    expect(url.searchParams.get('fields')).toBe('summary')
    expect(url.searchParams.get('cursor')).toBe('previous')
    expect(url.searchParams.has('offset')).toBe(false)
    expect(url.searchParams.has('include_total')).toBe(false)
    expect(fetchMock.mock.calls[0][1].signal).toBe(abort.signal)
  })

  it('forwards explicit offset zero and include_total on full-row pages', async () => {
    const page = { items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null, offset: 0, next_offset: null, total: 0 } }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page))
    vi.stubGlobal('fetch', fetchMock)
    expect(await new TorqueApiClient('/api/v1').listTaskPage({ offset: 0, include_total: true })).toEqual(page)
    const url = new URL(fetchMock.mock.calls[0][0], 'http://localhost')
    expect(url.searchParams.get('offset')).toBe('0')
    expect(url.searchParams.get('include_total')).toBe('true')
    expect(url.searchParams.has('fields')).toBe(false)
  })

  it('forwards signals through the existing resource page adapters', async () => {
    const page = { items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null } }
    const fetchMock = vi.fn().mockImplementation(async () => jsonResponse(page))
    vi.stubGlobal('fetch', fetchMock)
    const client = new TorqueApiClient('/api/v1')
    const signal = new AbortController().signal
    await Promise.all([
      client.pageRuns({}, signal), client.listProjects(undefined, {}, signal),
      client.listEpics({}, signal), client.listSprints({}, signal), client.listIssues({}, signal),
      client.listComments('task', 'T-1', {}, signal), client.listComments('project', 'PRJ-1', {}, signal),
    ])
    expect(fetchMock.mock.calls.every(([, init]) => init.signal === signal)).toBe(true)
  })
})

describe('remaining list pages', () => {
  const client = new TorqueApiClient('/api/v1')
  afterEach(() => vi.restoreAllMocks())
  it.each([
    ['models', () => client.listModels('peer', { cursor: 'next', include_total: true })],
    ['templates', () => client.listTemplates({ search: 'needle', cursor: 'next', include_total: true })],
    ['plans', () => client.listPlans({ cursor: 'next', include_total: true })],
    ['plan children', () => client.listPlanChildren('ROOT', 'phase', { cursor: 'next', include_total: true })],
    ['collections', () => client.listCollections('active', { cursor: 'next', include_total: true })],
    ['collection tasks', () => client.listCollectionTasks('COL', { cursor: 'next', include_total: true })],
    ['collection inbox', () => client.listInboxTasks({ cursor: 'next', include_total: true })],
    ['pending checkpoints', () => client.listPendingCheckpoints({ cursor: 'next', include_total: true })],
    ['task checkpoints', () => client.listTaskCheckpoints('ROOT', { cursor: 'next', include_total: true })],
    ['thread', () => client.getThread('thread', { cursor: 'next', include_total: true })],
  ] as const)('preserves one %s page without exhausting continuation', async (_name, list) => {
    const page = { items: [{ id: 'row' }], meta: { returned: 1, limit: 50, has_more: true, next_cursor: 'more', total: 205 } }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page))
    globalThis.fetch = fetchMock as unknown as typeof fetch
    expect(await list()).toEqual(page)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const url = new URL(fetchMock.mock.calls[0][0] as string, 'http://localhost')
    expect(url.searchParams.get('cursor')).toBe('next')
    expect(url.searchParams.get('include_total')).toBe('true')
  })
})


describe('run cohort adapters', () => {
  afterEach(() => vi.restoreAllMocks())
  it('preserves executor/profile and window filters on list, facets and time-series', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ items: [], meta: { has_more: false, next_cursor: null, returned: 0, limit: 50 } })))
    globalThis.fetch = fetchMock as unknown as typeof fetch
    const client = new TorqueApiClient('/api/v1')
    const cohort = { executor: 'cli,api', profile: 'codex,claude', status: 'done,failed', since: '2026-10-01T00:00:00Z', until: '2026-10-02T00:00:00Z' }
    const signal = new AbortController().signal
    await client.pageRuns(cohort, signal)
    await client.runFacets(cohort, 'status,executor,profile', signal)
    await client.runTimeSeries({ ...cohort, bucket: 'day' })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    for (const [request] of fetchMock.mock.calls) {
      const query = new URL(request, 'http://fixture').searchParams
      for (const [key, value] of Object.entries(cohort)) expect(query.get(key)).toBe(value)
      expect(query.has('include_total')).toBe(false)
    }
    expect(fetchMock.mock.calls[1][1].signal).toBe(signal)
  })
})
