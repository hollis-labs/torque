import { afterEach, expect, it, vi } from 'vitest'
import { TorqueApiClient } from './api'
afterEach(() => vi.unstubAllGlobals())
it('creates a Torque forum through the backend without sending ownership or credentials', async () => {
  const forum = { id: 'design', title: 'Design', namespace: 'app/torque/forum/design' }
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ forum }), { status: 201 }))
  vi.stubGlobal('fetch', fetch)
  const api = new TorqueApiClient('/api/v1')
  expect(await api.forumCreate('design', 'Design')).toEqual({ forum })
  const [url, options] = fetch.mock.calls[0]
  expect(url).toBe('/api/v1/forums')
  expect(options.method).toBe('POST')
  expect(JSON.parse(options.body)).toEqual({
    id: 'design',
    title: 'Design',
  })
  expect(options.headers.Authorization).toBeUndefined()
})
it('encodes forum filters and ids through the Torque API', async () => {
  const fetch = vi
    .fn()
    .mockResolvedValue(new Response('{"items":[],"has_more":false}', { status: 200 }))
  vi.stubGlobal('fetch', fetch)
  const api = new TorqueApiClient('/api/v1')
  await api.forumSearch('a b', 'literal & words', {
    author_kind: 'agent',
    cursor: 'c/2',
    since_seq: 5,
  })
  const [url, opts] = fetch.mock.calls[0]
  const parsed = new URL(url, 'http://localhost')
  expect(parsed.pathname).toBe('/api/v1/forums/a%20b/search')
  expect(parsed.searchParams.get('query')).toBe('literal & words')
  expect(parsed.searchParams.get('cursor')).toBe('c/2')
  expect(parsed.searchParams.get('since_seq')).toBe('5')
  expect(opts.headers.Authorization).toBeUndefined()
})
it('sends only the Markdown and optional parent id for human posts', async () => {
  const fetch = vi
    .fn()
    .mockImplementation(() => Promise.resolve(new Response('{"post":{"id":"p"}}', { status: 201 })))
  vi.stubGlobal('fetch', fetch)
  const api = new TorqueApiClient('/api/v1')
  await api.forumCreatePost('t1', 'Hello', 'p1')
  expect(fetch.mock.calls[0][0]).toBe('/api/v1/forums/topics/t1/posts')
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
    body_md: 'Hello',
    parent_post_id: 'p1',
  })
  await api.forumCreatePost('t1', 'Top level')
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ body_md: 'Top level' })
})

it('preserves explicit zero, empty root and false archive filters', async () => {
  const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{}')))
  vi.stubGlobal('fetch', fetch)
  const api = new TorqueApiClient('/api/v1')
  await api.forumsList({ namespace: 'app/torque/forum/general', category_id: '', archived: false })
  const url = new URL(fetch.mock.calls[0][0], 'http://localhost')
  expect(url.searchParams.get('category_id')).toBe('')
  expect(url.searchParams.get('archived')).toBe('false')
  await api.forumUpdate('category', 'planning', { sort_order: 0, description: '' })
  expect(fetch.mock.calls[1][1].method).toBe('PATCH')
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ sort_order: 0, description: '' })
  await api.forumAction('forum', 'work', 'move', { destination_category_id: '', sort_order: 0 })
  expect(fetch.mock.calls[2][0]).toBe('/api/v1/forums/work/move')
  expect(JSON.parse(fetch.mock.calls[2][1].body)).toEqual({
    destination_category_id: '',
    sort_order: 0,
  })
})

it('requires explicit cascade and never sends browser credentials for moderation', async () => {
  const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{}')))
  vi.stubGlobal('fetch', fetch)
  const api = new TorqueApiClient('/api/v1')
  await api.forumDelete('post', 'p1')
  await api.forumDelete('post', 'p1', true)
  expect(fetch.mock.calls[0][0]).toBe('/api/v1/forums/posts/p1')
  expect(fetch.mock.calls[0][1].method).toBe('DELETE')
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ cascade: false })
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ cascade: true })
  expect(fetch.mock.calls[1][1].headers.Authorization).toBeUndefined()
})
