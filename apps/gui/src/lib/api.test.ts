import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { TorqueApiClient, normalizeArtifactList } from './api'

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

describe('normalizeArtifactList', () => {
  it('unwraps the {artifacts: [...]} envelope', () => {
    const out = normalizeArtifactList({ artifacts: [ENVELOPE_RECORD] })
    expect(Array.isArray(out)).toBe(true)
    expect(out).toHaveLength(1)
    expect(out[0].id).toBe(1)
    expect(out[0].task_id).toBe('TASK-1')
  })

  it('accepts a bare array body', () => {
    const out = normalizeArtifactList([ENVELOPE_RECORD])
    expect(out).toHaveLength(1)
  })

  it('returns [] for null', () => {
    expect(normalizeArtifactList(null)).toEqual([])
  })

  it('returns [] for undefined', () => {
    expect(normalizeArtifactList(undefined)).toEqual([])
  })

  it('returns [] for empty object', () => {
    expect(normalizeArtifactList({})).toEqual([])
  })

  it('returns [] when artifacts key is not an array', () => {
    expect(normalizeArtifactList({ artifacts: null })).toEqual([])
    expect(normalizeArtifactList({ artifacts: 'nope' })).toEqual([])
    expect(normalizeArtifactList({ artifacts: { 0: ENVELOPE_RECORD } })).toEqual([])
  })

  it('returns [] for a string body', () => {
    expect(normalizeArtifactList('oops')).toEqual([])
  })
})

describe('TorqueApiClient.listArtifacts', () => {
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

  it('returns [] and hits nested route first when envelope is empty', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ artifacts: [] }))
    const out = await client.listArtifacts('TASK-1')
    expect(out).toEqual([])
    expect(calledUrls()).toEqual(['/api/v1/tasks/TASK-1/artifacts'])
  })

  it('normalizes a populated envelope on the nested route', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ artifacts: [ENVELOPE_RECORD] }))
    const out = await client.listArtifacts('TASK-1')
    expect(out).toHaveLength(1)
    expect(out[0].id).toBe(1)
  })

  it('falls back to the legacy query-string route when nested returns SPA HTML', async () => {
    fetchMock
      .mockResolvedValueOnce(htmlResponse())
      .mockResolvedValueOnce(jsonResponse({ artifacts: [ENVELOPE_RECORD] }))
    const out = await client.listArtifacts('TASK-1')
    expect(out).toHaveLength(1)
    const urls = calledUrls()
    expect(urls[0]).toBe('/api/v1/tasks/TASK-1/artifacts')
    expect(urls[1]).toBe('/api/v1/artifacts?task_id=TASK-1')
  })

  it('returns [] on 404 without falling back', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: 'not found' }, { status: 404 }))
    const out = await client.listArtifacts('GONE')
    expect(out).toEqual([])
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('returns [] on network error from both attempts', async () => {
    fetchMock.mockRejectedValue(new TypeError('NetworkError'))
    const out = await client.listArtifacts('TASK-1')
    expect(out).toEqual([])
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('returns [] when both responses are non-JSON garbage', async () => {
    fetchMock
      .mockResolvedValueOnce(htmlResponse())
      .mockResolvedValueOnce(htmlResponse())
    const out = await client.listArtifacts('TASK-1')
    expect(out).toEqual([])
  })

  it('handles a bare-array body on the nested route', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([ENVELOPE_RECORD]))
    const out = await client.listArtifacts('TASK-1')
    expect(out).toHaveLength(1)
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

  it('fetches all pages when no caller limit is supplied', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        tasks: [{ id: 'TASK-1' }, { id: 'TASK-2' }],
        total: 3,
        returned: 2,
        limit: 2,
        offset: 0,
        has_more: true,
        next_offset: 2,
        continuation: { limit: 2, offset: 2 },
      }))
      .mockResolvedValueOnce(jsonResponse({
        tasks: [{ id: 'TASK-3' }],
        total: 3,
        returned: 1,
        limit: 2,
        offset: 2,
        has_more: false,
        next_offset: null,
        continuation: null,
      }))

    const out = await client.listTasks({ status: ['todo'], tags: ['torque', 'api'] })

    expect(out.tasks.map((t) => t.id)).toEqual(['TASK-1', 'TASK-2', 'TASK-3'])
    expect(out.total).toBe(3)
    expect(calledUrls()).toEqual([
      '/api/v1/tasks?status=todo&tags=torque%2Capi&offset=0',
      '/api/v1/tasks?status=todo&tags=torque%2Capi&limit=2&offset=2',
    ])
  })

  it('treats an explicit limit as the caller cap across server-sized pages', async () => {
    const first = Array.from({ length: 200 }, (_, i) => ({ id: `TASK-${i + 1}` }))
    const second = Array.from({ length: 50 }, (_, i) => ({ id: `TASK-${i + 201}` }))
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        tasks: first,
        total: 400,
        returned: 200,
        limit: 200,
        offset: 10,
        has_more: true,
        next_offset: 210,
        continuation: { limit: 200, offset: 210 },
      }))
      .mockResolvedValueOnce(jsonResponse({
        tasks: second,
        total: 400,
        returned: 50,
        limit: 50,
        offset: 210,
        has_more: true,
      }))

    const out = await client.listTasks({ limit: 250, offset: 10, manual: true })

    expect(out.tasks).toHaveLength(250)
    expect(out.total).toBe(400)
    expect(calledUrls()).toEqual([
      '/api/v1/tasks?limit=200&offset=10&manual=true',
      '/api/v1/tasks?limit=50&offset=210&manual=true',
    ])
  })

  it('treats zero and negative caller limits as uncapped overall', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        tasks: [{ id: 'TASK-1' }],
        total: 1,
        returned: 1,
        limit: 200,
        offset: 0,
        has_more: false,
      }))
      .mockResolvedValueOnce(jsonResponse({
        tasks: [{ id: 'TASK-2' }],
        total: 1,
        returned: 1,
        limit: 200,
        offset: 0,
        has_more: false,
      }))

    await expect(client.listTasks({ limit: 0 })).resolves.toMatchObject({ total: 1 })
    await expect(client.listTasks({ limit: -1 })).resolves.toMatchObject({ total: 1 })
    expect(calledUrls()).toEqual([
      '/api/v1/tasks?offset=0',
      '/api/v1/tasks?offset=0',
    ])
  })

  it('rejects fractional and non-finite caller pagination values before fetch', async () => {
    await expect(client.listTasks({ limit: 1.5 })).rejects.toThrow(/limit must be/i)
    await expect(client.listTasks({ limit: Number.POSITIVE_INFINITY })).rejects.toThrow(/limit must be/i)
    await expect(client.listTasks({ offset: 0.5 })).rejects.toThrow(/offset must be/i)
    await expect(client.listTasks({ offset: -1 })).rejects.toThrow(/offset must be/i)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('rejects non-progressing continuation metadata', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({
      tasks: [{ id: 'TASK-1' }],
      total: 2,
      returned: 1,
      limit: 1,
      offset: 0,
      has_more: true,
      next_offset: 0,
    }))

    await expect(client.listTasks()).rejects.toThrow(/usable forward continuation/i)
  })

  it('rejects fractional continuation metadata', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({
      tasks: [{ id: 'TASK-1' }],
      total: 2,
      returned: 1,
      limit: 1,
      offset: 0,
      has_more: true,
      next_offset: 1.5,
    }))

    await expect(client.listTasks()).rejects.toThrow(/usable forward continuation/i)
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

  it('getInbox drains GET /messages/inbox and unwraps {messages}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ messages: [ENVELOPE] }))
    const out = await client.getInbox('msg://user/local/operator')
    expect(out).toHaveLength(1)
    expect(out[0].id).toBe('msg-1')
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/v1/messages/inbox?to=msg%3A%2F%2Fuser%2Flocal%2Foperator',
    )
  })

  it('getInbox returns [] when the envelope key is null', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ messages: null }))
    await expect(client.getInbox('msg://user/local/operator')).resolves.toEqual([])
  })

  it('getThread passes filter params to GET /messages/thread/{id}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ messages: [ENVELOPE] }))
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
