import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ClockworkApiClient, normalizeArtifactList } from './api'

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

describe('ClockworkApiClient.listArtifacts', () => {
  const client = new ClockworkApiClient('/api/v1')
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
