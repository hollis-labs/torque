import { describe, expect, it, vi } from 'vitest'
import { type FetchLike, ForgeError, HttpForgeClient, MockForgeClient, MOCK_REPO, createForgeClient, resolveForgeMode, mapForgeErrorCode } from './index'

const ref = { repo: MOCK_REPO, number: 271 }

function mk(body: unknown, init: { status?: number } = {}, opts: ConstructorParameters<typeof HttpForgeClient>[0] = {}) {
  const fetch = vi.fn<FetchLike>(async () => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status: init.status ?? 200 }))
  return { fetch, client: new HttpForgeClient({ baseUrl: '/api/v1/forge/', fetch, ...opts }) }
}
const call = (f: ReturnType<typeof mk>['fetch'], i = 0) => ({ url: f.mock.calls[i][0] as string, init: f.mock.calls[i][1] as RequestInit })

describe('HttpForgeClient reads', () => {
  it('getRepo hits the repo path and merges identity', async () => {
    const { fetch, client } = mk({ data: { defaultBranch: 'main', stars: 3, extra: 'x' } })
    const r = await client.getRepo(MOCK_REPO)
    expect(call(fetch).url).toBe('/api/v1/forge/repos/github/hollis-labs/torque')
    expect(r).toMatchObject({ host: 'github', owner: 'hollis-labs', defaultBranch: 'main', stars: 3, extra: 'x' })
  })

  it('listPrs builds query, paginates, defaults missing fields', async () => {
    const { fetch, client } = mk({ data: { items: [{ ref, title: 't', unknownField: 1 }], nextCursor: 'c2' } })
    const p = await client.listPrs({ repo: MOCK_REPO, state: 'open', assignee: 'a b', query: 'x', limit: 5, cursor: 'c1' })
    expect(call(fetch).url).toBe('/api/v1/forge/repos/github/hollis-labs/torque/pulls?state=open&assignee=a+b&q=x&limit=5&cursor=c1')
    expect(p.nextCursor).toBe('c2')
    expect(p.items[0]).toMatchObject({ title: 't', unknownField: 1, assignees: [], checks: [], reviews: [], mergeable: 'unknown' })
  })

  it('listPrs without repo uses the global path; bare array body tolerated', async () => {
    const { fetch, client } = mk([{ ref, title: 't' }])
    const p = await client.listPrs()
    expect(call(fetch).url).toBe('/api/v1/forge/pulls')
    expect(p.items).toHaveLength(1)
    expect(p.nextCursor).toBeNull()
  })

  it('getPr/getDiff/commits/comments/pending hit the proposed paths', async () => {
    const { fetch, client } = mk({ data: { items: [] } })
    await client.getPr(ref)
    await client.getDiff(ref)
    await client.listCommits(ref)
    await client.listComments(ref)
    await client.listPendingPrs()
    const base = '/api/v1/forge/repos/github/hollis-labs/torque/pulls/271'
    expect([0, 1, 2, 3, 4].map((i) => call(fetch, i).url)).toEqual([base, `${base}/diff`, `${base}/commits`, `${base}/comments`, '/api/v1/forge/pulls/pending'])
  })

  it('does not send identity headers on reads', async () => {
    const confirmToken = vi.fn(() => 'tok')
    const { fetch, client } = mk({ data: {} }, {}, { confirmToken })
    await client.getPr(ref)
    expect(confirmToken).not.toHaveBeenCalled()
    expect((call(fetch).init.headers as Record<string, string>)['X-Identity-Confirm']).toBeUndefined()
  })
})

describe('HttpForgeClient writes', () => {
  it('addComment posts body with identity token and idempotency key', async () => {
    const { fetch, client } = mk({ data: { kind: 'issue', id: '1', body: 'hi' } }, {}, { confirmToken: async () => 'tok', idempotencyKey: () => 'k1' })
    const c = await client.addComment(ref, { body: 'hi' })
    const { url, init } = call(fetch)
    expect(url).toMatch(/\/pulls\/271\/comments$/)
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ body: 'hi' })
    expect(init.headers).toMatchObject({ 'X-Identity-Confirm': 'tok', 'Idempotency-Key': 'k1', 'Content-Type': 'application/json' })
    expect(c.id).toBe('1')
  })

  it('reviewPr posts and assign PUTs', async () => {
    const r = mk({ data: { id: 'r1', state: 'approved' } })
    await r.client.reviewPr(ref, { verdict: 'approve' })
    expect(call(r.fetch).url).toMatch(/\/pulls\/271\/reviews$/)
    const a = mk({ data: { ref } })
    const pr = await a.client.assign(ref, { assignees: ['x'] })
    expect(call(a.fetch).init.method).toBe('PUT')
    expect(pr.assignees).toEqual([])
  })
})

describe('error mapping', () => {
  it.each([
    [{ error: { code: 'credential_missing', message: 'no token' } }, 500, 'unauthorized'],
    [{ error: { code: 'connector_unavailable' } }, 502, 'unavailable'],
    [{ code: 'invalid_args', message: 'bad' }, 400, 'invalid_args'],
    [{ error: { code: 'weird_new_code' } }, 400, 'weird_new_code'],
    [{}, 404, 'not_found'],
    [{}, 409, 'conflict'],
    [{}, 429, 'rate_limited'],
    [{}, 403, 'unauthorized'],
    [{}, 503, 'unavailable'],
    ['<html>', 502, 'unavailable'],
  ])('%j (%i) -> %s', async (body, status, code) => {
    const { client } = mk(body, { status })
    const err = await client.getPr(ref).catch((e) => e)
    expect(err).toBeInstanceOf(ForgeError)
    expect(err.code).toBe(code)
  })

  it('maps network failure and invalid 200 JSON to unavailable', async () => {
    const f = vi.fn<FetchLike>(async () => { throw new Error('offline') })
    const err = await new HttpForgeClient({ fetch: f }).getPr(ref).catch((e) => e)
    expect(err.code).toBe('unavailable')
    const bad = await mk('not json').client.getPr(ref).catch((e) => e)
    expect(bad.code).toBe('unavailable')
  })

  it('mapForgeErrorCode passes known codes through', () => {
    expect(mapForgeErrorCode('conflict', 500)).toBe('conflict')
  })
})

describe('factory', () => {
  it('defaults to mock; query beats env', () => {
    expect(resolveForgeMode()).toBe('mock')
    expect(resolveForgeMode({ env: 'http' })).toBe('http')
    expect(resolveForgeMode({ search: '?forge=mock', env: 'http' })).toBe('mock')
    expect(resolveForgeMode({ search: '?forge=bogus' })).toBe('mock')
    expect(createForgeClient()).toBeInstanceOf(MockForgeClient)
    expect(createForgeClient({ search: '?forge=http' })).toBeInstanceOf(HttpForgeClient)
  })
})
