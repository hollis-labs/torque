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
    forumActivity: vi.fn(),
    forumCreate: vi.fn(),
    forumCategories: vi.fn(),
    forumCategory: vi.fn(),
    forumUpdate: vi.fn(),
    forumAction: vi.fn(),
    forumDelete: vi.fn(),
    forumCreateCategory: vi.fn(),
    forumParticipants: vi.fn(),
    forumGrant: vi.fn(),
    forumRevoke: vi.fn(),
    forumSetOwner: vi.fn(),
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
  api.forumCategories.mockResolvedValue({ items: [], has_more: false })
  api.forumActivity.mockResolvedValue({ post_count: 0, last_post: null })
  api.forumUpdate.mockResolvedValue({})
  api.forumDelete.mockResolvedValue({ deleted: 'p1' })
  api.forumAction.mockResolvedValue({})
  api.forumParticipants.mockResolvedValue({ items: [], has_more: false })
  api.forumGet.mockResolvedValue({ forum: { id: 'portfolio', title: 'Portfolio' } })
  api.forumTopics.mockResolvedValue({ items: [topic], has_more: false })
  api.forumSearch.mockResolvedValue({ items: [post], has_more: false })
  api.forumTopic.mockResolvedValue({
    topic,
    outline: { items: [post], last_seq: 3, has_more: false },
  })
  api.forumPosts.mockResolvedValue({ items: [], has_more: false })
  api.forumPost.mockResolvedValue({
    post: { ...post, body_md: '**Expanded Markdown**', truncated: false },
  })
  api.forumCreateTopic.mockResolvedValue({ topic })
  api.forumCreatePost.mockResolvedValue({
    post: { ...post, id: 'p2', seq: 4, snippet: 'My reply' },
  })
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})
it('creates a forum from the home modal and preserves the existing directory', async () => {
  const forum = {
    id: 'design-review',
    title: 'Design Review',
    namespace: 'app/torque/forum/design-review',
  }
  api.forumCreate.mockImplementation(async () => {
    api.forumsList.mockResolvedValue({
      items: [
        forum,
        { id: 'portfolio', title: 'Portfolio', namespace: 'app/torque/forum/portfolio' },
      ],
      has_more: false,
    })
    return { forum }
  })
  renderAt()
  await screen.findByText('Portfolio')
  fireEvent.click(screen.getByRole('button', { name: 'New Forum' }))
  await screen.findByRole('dialog', { name: 'New Forum' })
  expect(screen.getByRole('button', { name: 'Create forum' }).hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('Forum name'), { target: { value: '  Design Review  ' } })
  expect((screen.getByLabelText('Short name') as HTMLInputElement).value).toBe('design-review')
  fireEvent.click(screen.getByRole('button', { name: 'Create forum' }))
  await screen.findByRole('heading', { name: 'Design Review' })
  expect(api.forumCreate).toHaveBeenCalledWith('design-review', 'Design Review', {
    category_id: '',
    description: '',
    sort_order: 0,
  })
  expect(screen.getByRole('heading', { name: 'Portfolio' })).toBeTruthy()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})
it('validates short names and keeps a failed creation open for correction and retry', async () => {
  api.forumCreate
    .mockRejectedValueOnce(new ApiError(409, 'Forum already exists.'))
    .mockImplementationOnce(async () => {
      const forum = {
        id: 'design-team',
        title: 'Design',
        namespace: 'app/torque/forum/design-team',
      }
      api.forumsList.mockResolvedValue({ items: [forum], has_more: false })
      return { forum }
    })
  renderAt()
  await screen.findByText('Portfolio')
  fireEvent.click(screen.getByRole('button', { name: 'New Forum' }))
  fireEvent.change(screen.getByLabelText('Forum name'), { target: { value: 'Design' } })
  fireEvent.change(screen.getByLabelText('Short name'), { target: { value: 'invalid/name' } })
  expect(screen.getByRole('button', { name: 'Create forum' }).hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('Short name'), { target: { value: 'topics' } })
  expect(screen.getByText('This short name is reserved. Please choose another.')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Create forum' }).hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('Short name'), { target: { value: '_design' } })
  expect(screen.getByRole('button', { name: 'Create forum' }).hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('Short name'), { target: { value: 'design' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create forum' }))
  expect((await screen.findByRole('alert')).textContent).toBe('Forum already exists.')
  expect((screen.getByLabelText('Forum name') as HTMLInputElement).value).toBe('Design')
  fireEvent.change(screen.getByLabelText('Short name'), { target: { value: 'design-team' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create forum' }))
  await screen.findByRole('heading', { name: 'Design' })
  expect(api.forumCreate).toHaveBeenLastCalledWith('design-team', 'Design', {
    category_id: '',
    description: '',
    sort_order: 0,
  })
})
it('cancels without creating a forum and resets the modal when reopened', async () => {
  renderAt()
  await screen.findByText('Portfolio')
  fireEvent.click(screen.getByRole('button', { name: 'New Forum' }))
  fireEvent.change(screen.getByLabelText('Forum name'), { target: { value: 'Discard this' } })
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(api.forumCreate).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'New Forum' }))
  expect((screen.getByLabelText('Forum name') as HTMLInputElement).value).toBe('')
  expect((screen.getByLabelText('Short name') as HTMLInputElement).value).toBe('')
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
    .mockResolvedValueOnce({
      items: [{ ...topic, id: 't2', title: 'Second topic' }],
      has_more: false,
    })
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
  fireEvent.click(screen.getByText('Filters'))
  fireEvent.change(screen.getByLabelText('Author kind filter'), { target: { value: 'agent' } })
  fireEvent.click(screen.getByText('Apply filters'))
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
  fireEvent.click(screen.getByRole('button', { name: 'New topic' }))
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
  await waitFor(() =>
    expect(api.forumCreatePost).toHaveBeenLastCalledWith('t1', 'Top level', undefined),
  )
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

it('groups forums by category and sorts all pages before rendering', async () => {
  const ns = 'app/torque/forum/general'
  api.forumCategories.mockResolvedValue({
    items: [{ id: 'planning', namespace: ns, title: 'Planning', sort_order: 10 }],
    has_more: false,
  })
  api.forumsList
    .mockResolvedValueOnce({
      items: [{ id: 'z', namespace: ns, category_id: 'planning', title: 'Later', sort_order: 20 }],
      has_more: true,
      next_cursor: 'board-page-2',
    })
    .mockResolvedValueOnce({
      items: [
        { id: 'a', namespace: ns, category_id: 'planning', title: 'Earlier', sort_order: 10 },
      ],
      has_more: false,
    })
  renderAt()
  await screen.findByRole('heading', { name: 'Earlier' })
  const group = screen.getByRole('region', { name: 'Planning' })
  expect(group.textContent!.indexOf('Earlier')).toBeLessThan(group.textContent!.indexOf('Later'))
  expect(api.forumsList).toHaveBeenLastCalledWith(
    expect.objectContaining({ cursor: 'board-page-2', limit: 100 }),
    expect.any(AbortSignal),
  )
  fireEvent.click(screen.getByRole('button', { name: 'Manage Planning' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Sort order'), { target: { value: '0' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() =>
    expect(api.forumUpdate).toHaveBeenCalledWith('category', 'planning', { sort_order: 0 }),
  )
  await waitFor(() => expect(api.forumsList.mock.calls.length).toBeGreaterThan(2))
  expect(api.forumsList.mock.calls[2][0].cursor).toBeUndefined()
})

it('edits the full message, clears optional fields and refreshes the outline', async () => {
  api.forumPost.mockResolvedValue({
    post: {
      ...post,
      body_md: 'Complete original body',
      subject: 'Old subject',
      attachments: [{ name: 'Old', uri: 'https://example.com/old' }],
      truncated: false,
    },
  })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Post #3' }))
  await waitFor(() =>
    expect((screen.getByLabelText('Message Markdown') as HTMLTextAreaElement).value).toBe(
      'Complete original body',
    ),
  )
  fireEvent.change(screen.getByLabelText('Message Markdown'), {
    target: { value: 'Edited complete body' },
  })
  fireEvent.change(screen.getByLabelText('Subject'), { target: { value: '' } })
  fireEvent.click(screen.getByRole('button', { name: 'Remove attachment 1' }))
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() =>
    expect(api.forumUpdate).toHaveBeenCalledWith('post', 'p1', {
      body_md: 'Edited complete body',
      subject: '',
      attachments: [],
    }),
  )
  await waitFor(() => expect(api.forumTopic).toHaveBeenCalledTimes(2))
})

it('refuses to save an incomplete body returned by full expansion', async () => {
  api.forumPost.mockResolvedValue({
    post: { ...post, body_md: 'Still truncated', truncated: true },
  })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Post #3' }))
  expect((await screen.findByRole('alert')).textContent).toContain(
    'Editing a preview would lose content',
  )
  expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(true)
  expect(api.forumUpdate).not.toHaveBeenCalled()
})

it('keeps a conflicting delete open and requires explicit cascade consent', async () => {
  api.forumDelete
    .mockRejectedValueOnce(new ApiError(409, 'Post has replies; use cascade to delete them.'))
    .mockResolvedValueOnce({ deleted: 'p1' })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Post #3' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'delete' } })
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull())
  expect(screen.getByRole('button', { name: 'Delete permanently' }).hasAttribute('disabled')).toBe(
    true,
  )
  fireEvent.click(screen.getByLabelText('I confirm permanent deletion of this post'))
  fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }))
  await screen.findByText('Post has replies; use cascade to delete them.')
  expect(api.forumDelete).toHaveBeenCalledWith('post', 'p1', false)
  expect(screen.getByRole('dialog')).toBeTruthy()
  fireEvent.click(
    screen.getByLabelText('Also delete all contained boards, threads, messages and replies'),
  )
  api.forumTopic.mockResolvedValue({
    topic: { ...topic, post_count: 0 },
    outline: { items: [], has_more: false },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }))
  await waitFor(() => expect(api.forumDelete).toHaveBeenLastCalledWith('post', 'p1', true))
  await screen.findByText('No posts yet')
  expect(screen.queryByText('Compact preview')).toBeNull()
})

it('archives and restores a thread without changing its resolved status', async () => {
  api.forumTopic.mockResolvedValue({
    topic: { ...topic, status: 'resolved' },
    outline: { items: [post], has_more: false },
  })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Pilot topic' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'archive' } })
  api.forumTopic.mockResolvedValue({
    topic: { ...topic, status: 'resolved', archived: true },
    outline: { items: [post], has_more: false },
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() => expect(api.forumAction).toHaveBeenCalledWith('topic', 't1', 'archive', {}))
  await screen.findByText(/Restore the archived item/)
  expect(screen.queryByLabelText('Post Markdown')).toBeNull()
  expect(screen.getByText('resolved')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Manage Pilot topic' }))
  // An explicitly archived item opens with Restore selected.
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull())
  api.forumTopic.mockResolvedValue({
    topic: { ...topic, status: 'resolved', archived: false },
    outline: { items: [post], has_more: false },
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await screen.findByLabelText('Post Markdown')
  expect(api.forumAction).toHaveBeenLastCalledWith('topic', 't1', 'unarchive', {})
})

it('blocks descendant writing when only the category is archived', async () => {
  api.forumGet.mockResolvedValue({
    forum: { id: 'portfolio', title: 'Portfolio', category_id: 'planning', archived: false },
  })
  api.forumCategory.mockResolvedValue({ category: { id: 'planning', archived: true } })
  renderAt('/forums/portfolio/topics/t1')
  await screen.findByText(/Restore the archived item/)
  expect(screen.queryByLabelText('Post Markdown')).toBeNull()
  expect(screen.getByRole('button', { name: 'Reply' }).hasAttribute('disabled')).toBe(true)
  expect(api.forumCategory).toHaveBeenCalledWith('planning', expect.any(AbortSignal))
})

it('moves a message subtree into a selected thread in the same namespace', async () => {
  const ns = 'app/torque/forum/general'
  api.forumGet.mockResolvedValue({ forum: { id: 'portfolio', title: 'Portfolio', namespace: ns } })
  api.forumsList.mockResolvedValue({
    items: [{ id: 'destination', title: 'Destination board', namespace: ns }],
    has_more: false,
  })
  api.forumTopics.mockResolvedValue({
    items: [{ ...topic, id: 't2', forum_id: 'destination', title: 'Destination thread' }],
    has_more: false,
  })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Post #3' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'move' } })
  await screen.findByRole('option', { name: 'Destination board' })
  fireEvent.change(screen.getByLabelText('Destination'), { target: { value: 'destination' } })
  await screen.findByRole('option', { name: 'Destination thread' })
  fireEvent.change(screen.getByLabelText('Destination thread'), { target: { value: 't2' } })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() =>
    expect(api.forumAction).toHaveBeenCalledWith('post', 'p1', 'move', {
      destination_topic_id: 't2',
    }),
  )
  expect(api.forumsList).toHaveBeenCalledWith(
    expect.objectContaining({ archived: false }),
    expect.any(AbortSignal),
  )
  await waitFor(() => expect(api.forumTopic).toHaveBeenCalledTimes(2))
})

it('adds subjects and attachment references without exposing unsafe links', async () => {
  api.forumTopic.mockResolvedValue({
    topic,
    outline: {
      items: [
        {
          ...post,
          subject: 'A subject',
          attachments: [
            { name: 'Unsafe', uri: 'javascript:alert(1)' },
            { name: 'Design', uri: 'https://example.com/design' },
          ],
        },
      ],
      has_more: false,
    },
  })
  renderAt('/forums/portfolio/topics/t1')
  await screen.findByRole('heading', { name: 'A subject' })
  expect(screen.queryByRole('link', { name: 'Unsafe' })).toBeNull()
  expect(screen.getByRole('link', { name: 'Design' }).getAttribute('href')).toBe(
    'https://example.com/design',
  )
  fireEvent.change(screen.getByLabelText('Post subject'), { target: { value: 'New subject' } })
  fireEvent.change(screen.getByLabelText('Post Markdown'), { target: { value: 'See attached' } })
  fireEvent.click(screen.getByRole('button', { name: 'Add attachment' }))
  fireEvent.change(screen.getByLabelText('Attachment 1 name'), { target: { value: 'Spec' } })
  fireEvent.change(screen.getByLabelText('Attachment 1 URL'), {
    target: { value: 'https://example.com/spec' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Post' }))
  await waitFor(() =>
    expect(api.forumCreatePost).toHaveBeenCalledWith('t1', 'See attached', undefined, {
      subject: 'New subject',
      attachments: [{ name: 'Spec', uri: 'https://example.com/spec' }],
    }),
  )
})

it('copies a board with a new ID and can place the copy at the namespace root', async () => {
  renderAt('/forums/portfolio')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Portfolio' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'clone' } })
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull())
  fireEvent.change(screen.getByLabelText('New board ID'), { target: { value: 'portfolio-copy' } })
  fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Portfolio copy' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() =>
    expect(api.forumAction).toHaveBeenCalledWith('forum', 'portfolio', 'clone', {
      id: 'portfolio-copy',
      title: 'Portfolio copy',
      destination_category_id: '',
    }),
  )
})

it('forks a selected message using its source thread and keeps the original discussion', async () => {
  const ns = 'app/torque/forum/general'
  api.forumGet.mockResolvedValue({ forum: { id: 'portfolio', title: 'Portfolio', namespace: ns } })
  api.forumsList.mockResolvedValue({
    items: [{ id: 'portfolio', title: 'Portfolio', namespace: ns }],
    has_more: false,
  })
  renderAt('/forums/portfolio/topics/t1')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Post #3' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(
      false,
    ),
  )
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'fork' } })
  await screen.findByRole('option', { name: 'Portfolio' })
  fireEvent.change(screen.getByLabelText('Destination'), { target: { value: 'portfolio' } })
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull())
  fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Separate discussion' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() =>
    expect(api.forumAction).toHaveBeenCalledWith('topic', 't1', 'fork', {
      post_id: 'p1',
      destination_forum_id: 'portfolio',
      title: 'Separate discussion',
    }),
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(screen.getByText('Compact preview')).toBeTruthy()
})

it('grants participant access and requires confirmation to transfer ownership', async () => {
  api.forumGrant.mockResolvedValue({})
  api.forumSetOwner.mockResolvedValue({ forum: { id: 'portfolio', title: 'Portfolio' } })
  renderAt('/forums/portfolio')
  fireEvent.click(await screen.findByRole('button', { name: 'Access' }))
  await waitFor(() => expect(screen.queryByText('Loading participants…')).toBeNull())
  fireEvent.change(screen.getByLabelText('Participant ID'), {
    target: { value: 'urn:agent:manager' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Grant / update access' }))
  await waitFor(() =>
    expect(api.forumGrant).toHaveBeenCalledWith('portfolio', {
      principal_kind: 'agent',
      principal_id: 'urn:agent:manager',
      access: 'post',
    }),
  )
  fireEvent.click(screen.getByText('Transfer ownership', { selector: 'summary' }))
  expect(screen.getByRole('button', { name: 'Transfer ownership' }).hasAttribute('disabled')).toBe(
    true,
  )
  fireEvent.click(screen.getByLabelText('Confirm ownership transfer'))
  fireEvent.click(screen.getByRole('button', { name: 'Transfer ownership' }))
  await waitFor(() => expect(api.forumSetOwner).toHaveBeenCalledWith('portfolio', {}))
})

it('shows category descriptions, totals and the last author without namespace controls', async () => {
  api.forumCategories.mockResolvedValue({ items: [{ id: 'work', title: 'Work', description: 'Our projects', namespace: 'app/torque/forum/torque' }], has_more: false })
  api.forumsList.mockResolvedValue({ items: [{ id: 'portfolio', title: 'Portfolio', description: 'Planning together', category_id: 'work', namespace: 'app/torque/forum/torque' }], has_more: false })
  api.forumActivity.mockResolvedValue({ post_count: 12, last_post: { id: 'latest', topic_id: 't1', created_at: '2026-10-13T12:30:00Z', provenance: { id: 'Chrispian', kind: 'user' } } })
  renderAt('/forums')
  await screen.findByText('Chrispian')
  expect(screen.getByText('Our projects')).toBeTruthy()
  expect(screen.getByText('Planning together')).toBeTruthy()
  expect(screen.getByLabelText('Portfolio post count').textContent).toBe('12')
  expect(screen.getByText('Chrispian').closest('a')?.getAttribute('href')).toBe('/forums/portfolio/topics/t1#post-latest')
  expect(screen.getByText(new Date('2026-10-13T12:30:00Z').toLocaleString())).toBeTruthy()
  expect(screen.queryByText(/app\/torque/)).toBeNull()
  expect(screen.queryByLabelText(/namespace/i)).toBeNull()
})

it('moves an existing forum into a category without selecting a namespace', async () => {
  api.forumCategories.mockResolvedValue({ items: [{ id: 'work', title: 'Work', namespace: 'app/torque/forum/torque' }], has_more: false })
  renderAt('/forums')
  fireEvent.click(await screen.findByRole('button', { name: 'Manage Portfolio' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(false))
  fireEvent.change(screen.getByLabelText('Management action'), { target: { value: 'move' } })
  await screen.findByRole('option', { name: 'Work' })
  fireEvent.change(screen.getByLabelText('Destination'), { target: { value: 'work' } })
  await waitFor(() => expect(screen.getByRole('button', { name: 'Apply changes' }).hasAttribute('disabled')).toBe(false))
  fireEvent.click(screen.getByRole('button', { name: 'Apply changes' }))
  await waitFor(() => expect(api.forumAction).toHaveBeenCalledWith('forum', 'portfolio', 'move', { destination_category_id: 'work' }))
  expect(api.forumCategories).toHaveBeenCalledWith(expect.not.objectContaining({ namespace: expect.anything() }), expect.any(AbortSignal))
})
