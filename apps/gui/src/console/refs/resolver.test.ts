import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMockResolver, createRefResolverClient, createTorqueTaskResolver } from './index'

const ent = (title: string) => ({ title })
beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

function setup(extra: Record<string, unknown> = {}) {
  const calls: string[][] = []
  const resolver = createMockResolver({ A: ent('a'), B: ent('b'), C: ent('c') }, { onCall: (c) => calls.push(c) })
  const client = createRefResolverClient({ resolvers: { k: resolver }, ...extra })
  return { calls, client }
}
const flush = () => vi.advanceTimersByTimeAsync(20)

describe('resolver client', () => {
  it('starts idle, goes loading, then found', async () => {
    const { client } = setup()
    expect(client.get('k', 'A')).toEqual({ status: 'idle' })
    client.request([{ kind: 'k', canonical: 'A' }])
    expect(client.get('k', 'A').status).toBe('loading')
    await flush()
    expect(client.get('k', 'A')).toMatchObject({ status: 'found', entity: { title: 'a' } })
  })
  it('batches requests in a window into one call and dedupes', async () => {
    const { client, calls } = setup()
    client.request([{ kind: 'k', canonical: 'A' }, { kind: 'k', canonical: 'B' }])
    client.request([{ kind: 'k', canonical: 'A' }, { kind: 'k', canonical: 'C' }])
    await flush()
    expect(calls).toEqual([['A', 'B', 'C']])
  })
  it('splits oversized batches', async () => {
    const { client, calls } = setup({ maxBatch: 2 })
    client.request(['A', 'B', 'C'].map((canonical) => ({ kind: 'k', canonical })))
    await flush()
    expect(calls.map((c) => c.length)).toEqual([2, 1])
  })
  it('caches within ttl, refetches after it', async () => {
    const { client, calls } = setup({ ttlMs: 1000 })
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    expect(calls.length).toBe(1)
    await vi.advanceTimersByTimeAsync(1500)
    client.request([{ kind: 'k', canonical: 'A' }])
    // stale entity is kept while reloading
    expect(client.get('k', 'A')).toMatchObject({ status: 'loading', stale: { title: 'a' } })
    await flush()
    expect(calls.length).toBe(2)
  })
  it('does not re-request while in flight', async () => {
    const { client, calls } = setup()
    client.request([{ kind: 'k', canonical: 'A' }])
    await vi.advanceTimersByTimeAsync(10)
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    expect(calls.length).toBe(1)
  })
  it('missing results are not-found, with a shorter ttl', async () => {
    const { client, calls } = setup({ notFoundTtlMs: 100 })
    client.request([{ kind: 'k', canonical: 'ZZZ' }])
    await flush()
    expect(client.get('k', 'ZZZ').status).toBe('not-found')
    await vi.advanceTimersByTimeAsync(200)
    client.request([{ kind: 'k', canonical: 'ZZZ' }])
    await flush()
    expect(calls.length).toBe(2)
  })
  it('errors fail the batch, keep stale data, and retry after the backoff', async () => {
    let fail = true
    const client = createRefResolverClient({
      resolvers: { k: { resolveMany: async (cs) => { if (fail) throw new Error('down'); return Object.fromEntries(cs.map((c) => [c, { status: 'found' as const, entity: ent(c) }])) } } },
      errorRetryMs: 1000,
    })
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    expect(client.get('k', 'A')).toMatchObject({ status: 'error', message: 'down' })
    client.request([{ kind: 'k', canonical: 'A' }]) // inside backoff: no new call
    expect(client.get('k', 'A').status).toBe('error')
    fail = false
    await vi.advanceTimersByTimeAsync(1500)
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    expect(client.get('k', 'A').status).toBe('found')
  })
  it('kinds without a resolver are unsupported, never fetched', () => {
    const client = createRefResolverClient({ resolvers: {} })
    client.request([{ kind: 'url', canonical: 'https://e.com' }])
    expect(client.get('url', 'https://e.com').status).toBe('unsupported')
  })
  it('notifies subscribers and gives stable snapshots between changes', async () => {
    const { client } = setup()
    const fn = vi.fn()
    const off = client.subscribe(fn)
    client.request([{ kind: 'k', canonical: 'A' }])
    const s1 = client.get('k', 'A')
    expect(client.get('k', 'A')).toBe(s1)
    await flush()
    expect(fn).toHaveBeenCalled()
    off()
    const n = fn.mock.calls.length
    client.invalidate()
    expect(fn.mock.calls.length).toBe(n)
  })
  it('invalidate re-resolves resolved refs', async () => {
    const { client, calls } = setup()
    client.request([{ kind: 'k', canonical: 'A' }])
    await flush()
    client.invalidate('k', 'A')
    await flush()
    expect(calls.length).toBe(2)
  })
  it('dispose cancels pending work and ignores later requests', async () => {
    const { client, calls } = setup()
    client.request([{ kind: 'k', canonical: 'A' }])
    client.dispose()
    client.request([{ kind: 'k', canonical: 'B' }])
    await flush()
    expect(calls).toEqual([])
  })
})

describe('torque task adapter', () => {
  it('maps task briefs and not-found', async () => {
    const r = createTorqueTaskResolver(async () => ({
      'CW-1': { id: 'CW-1', title: 'Do it', status: 'blocked', priority: 2, updated_at: '2026-10-16T00:00:00Z' },
      'CW-2': null,
    }))
    const out = await r.resolveMany(['CW-1', 'CW-2', 'CW-3'], { signal: new AbortController().signal })
    expect(out['CW-1']).toMatchObject({ status: 'found', entity: { title: 'Do it', status: { tone: 'danger' } } })
    expect(out['CW-2']).toEqual({ status: 'not-found' })
    expect(out['CW-3']).toEqual({ status: 'not-found' })
  })
})
