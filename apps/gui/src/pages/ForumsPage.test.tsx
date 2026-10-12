/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ApiError } from '@/lib/api'
import type { ForumPost, ForumTopic } from '@/lib/forums'
const { api } = vi.hoisted(() => ({
  api: {
    getFeatureFlags: vi.fn(),
    forumsList: vi.fn(),
    forumGet: vi.fn(),
    forumTopics: vi.fn(),
    forumSearch: vi.fn(),
    forumTopic: vi.fn(),
    forumPosts: vi.fn(),
    forumPost: vi.fn(),
    forumCreateTopic: vi.fn(),
    forumCreatePost: vi.fn(),
  },
}))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
import ForumsPage from './ForumsPage'
const topic: ForumTopic = {
  id: 't1',
  forum_id: 'portfolio',
  title: 'Pilot topic',
  status: 'open',
  created_by: { id: 'operator', kind: 'user' },
  post_count: 1,
  created_at: '2026-10-12T12:00:00Z',
  last_activity_at: '',
  last_seq: 999,
}
const post: ForumPost = {
  id: 'p1',
  topic_id: 't1',
  forum_id: 'portfolio',
  seq: 3,
  snippet: 'Compact preview',
  depth: 1,
  parent_post_id: 'p0',
  body_chars: 60,
  reply_count: 0,
  truncated: true,
  created_at: '',
  provenance: { id: 'operator', kind: 'user', verified: true },
}
function renderAt(path = '/forums') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/forums" element={<ForumsPage />} />
        <Route path="/forums/:forumId" element={<ForumsPage />} />
        <Route path="/forums/:forumId/topics/:topicId" element={<ForumsPage />} />
      </Routes>
    </MemoryRouter>,
  )
}
beforeEach(() => {
  vi.resetAllMocks()
  api.getFeatureFlags.mockResolvedValue({ forums: true })
  api.forumsList.mockResolvedValue({
    items: [{ id: 'portfolio', namespace: 'app/torque/forum/portfolio', title: 'Portfolio' }],
    has_more: false,
  })
  api.forumGet.mockResolvedValue({ forum: { id: 'portfolio', title: 'Portfolio' } })
  api.forumTopics.mockResolvedValue({ items: [topic], has_more: false })
  api.forumSearch.mockResolvedValue({ items: [post], has_more: false })
  api.forumTopic.mockResolvedValue({ topic, outline: { items: [post], last_seq: 3, has_more: false } })
  api.forumPosts.mockResolvedValue({ items: [], has_more: false })
  api.forumPost.mockResolvedValue({ post: { ...post, body_md: '**Expanded Markdown**', truncated: false } })
  api.forumCreateTopic.mockResolvedValue({ topic })
  api.forumCreatePost.mockResolvedValue({ post: { ...post, id: 'p2', seq: 4, snippet: 'My reply' } })
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})
it('gates the feature without contacting Tether and shows a degraded retry state', async () => {
  api.getFeatureFlags.mockResolvedValueOnce({ forums: false })
  const view = renderAt()
  await screen.findByText(/Forums is disabled/)
  expect(api.forumsList).not.toHaveBeenCalled()
  view.unmount()
  api.forumsList.mockRejectedValueOnce(new ApiError(503, 'Forums unavailable: check Tether.'))
  renderAt()
  expect((await screen.findByRole('alert')).textContent).toContain('Forums unavailable')
  fireEvent.click(screen.getByText('Retry'))
  await screen.findByText('Portfolio')
})
it('browses topics and preserves filters on pagination and search', async () => {
  api.forumTopics
    .mockResolvedValueOnce({ items: [topic], has_more: true, next_cursor: 'topic-page2' })
    .mockResolvedValueOnce({ items: [{ ...topic, id: 't2', title: 'Second topic' }], has_more: false })
  renderAt()
  fireEvent.click(await screen.findByText('Portfolio'))
  await screen.findByText('Pilot topic')
  fireEvent.click(screen.getByText('Load more'))
  await screen.findByText('Second topic')
  expect(api.forumTopics).toHaveBeenLastCalledWith(
    'portfolio',
    { limit: 20, cursor: 'topic-page2' },
    undefined,
  )
  fireEvent.change(screen.getByLabelText('Author kind filter'), { target: { value: 'agent' } })
  await waitFor(() =>
    expect(api.forumTopics).toHaveBeenLastCalledWith(
      'portfolio',
      { author_kind: 'agent', limit: 20 },
      expect.any(AbortSignal),
    ),
  )
  fireEvent.change(screen.getByLabelText('Search within forum'), { target: { value: 'contracts' } })
  fireEvent.click(screen.getByText('Search'))
  await screen.findByText('Compact preview')
  expect(api.forumSearch).toHaveBeenCalledWith(
    'portfolio',
    'contracts',
    { author_kind: 'agent', limit: 20 },
    expect.any(AbortSignal),
  )
})
it('creates topics and expands individual Markdown bodies on demand', async () => {
  renderAt('/forums/portfolio')
  await screen.findByText('Pilot topic')
  fireEvent.change(screen.getByLabelText('New topic title'), { target: { value: 'New topic' } })
  fireEvent.click(screen.getByText('Create topic'))
  await screen.findByText('Compact preview')
  expect(api.forumCreateTopic).toHaveBeenCalledWith('portfolio', 'New topic')
  expect(api.forumPost).not.toHaveBeenCalled()
  fireEvent.click(screen.getByText('Expand body'))
  const expanded = await screen.findByText('Expanded Markdown')
  expect(expanded.tagName).toBe('STRONG')
  expect(api.forumPost).toHaveBeenCalledWith('p1')
})
it('posts replies with parent id and top-level posts without attribution fields', async () => {
  renderAt('/forums/portfolio/topics/t1')
  await screen.findByText('Compact preview')
  fireEvent.click(screen.getByText('Reply'))
  fireEvent.change(screen.getByLabelText('Post Markdown'), { target: { value: 'My reply' } })
  fireEvent.click(screen.getByText('Post reply'))
  await screen.findByText('My reply')
  expect(api.forumCreatePost).toHaveBeenCalledWith('t1', 'My reply', 'p1')
  fireEvent.change(screen.getByLabelText('Post Markdown'), { target: { value: 'Top level' } })
  fireEvent.click(screen.getByRole('button', { name: /^Post$/ }))
  await waitFor(() => expect(api.forumCreatePost).toHaveBeenLastCalledWith('t1', 'Top level', undefined))
})
it('drains the outline then polls using delivered sequence instead of topic last_seq', async () => {
  api.forumTopic.mockResolvedValueOnce({
    topic,
    outline: { items: [post], last_seq: 3, has_more: true, next_cursor: 'outline2' },
  })
  api.forumPosts
    .mockResolvedValueOnce({
      items: [{ ...post, id: 'p2', seq: 7, snippet: 'Second preview' }],
      last_seq: 7,
      has_more: false,
    })
    .mockResolvedValue({ items: [], has_more: false })
  renderAt('/forums/portfolio/topics/t1')
  await screen.findByText('Compact preview')
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  await vi.advanceTimersByTimeAsync(10000)
  expect(api.forumPosts).not.toHaveBeenCalled()
  fireEvent.click(screen.getByText('Load more'))
  await screen.findByText('Second preview')
  await vi.advanceTimersByTimeAsync(10000)
  await waitFor(() =>
    expect(api.forumPosts).toHaveBeenLastCalledWith(
      't1',
      { since_seq: 7, cursor: undefined, limit: 20 },
      expect.any(AbortSignal),
    ),
  )
})
