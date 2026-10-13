import { expect, it, vi } from 'vitest'
import { drainPostPoll, mergePosts } from './forums'
import type { ForumPost } from './forums'
const post = (id: string, seq: number): ForumPost => ({
  id,
  seq,
  topic_id: 't',
  forum_id: 'f',
  depth: 0,
  reply_count: 0,
  body_chars: 5,
  truncated: true,
  created_at: '',
  provenance: { id: 'operator', kind: 'user', verified: true },
})
it('drains a poll with the original since_seq before returning its checkpoint', async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce({ items: [post('p1', 8)], last_seq: 8, has_more: true, next_cursor: 'page2' })
    .mockResolvedValueOnce({ items: [post('p2', 11)], last_seq: 11, has_more: false })
  const batch = await drainPostPoll(fetch, 5)
  expect(fetch).toHaveBeenNthCalledWith(1, { since_seq: 5, cursor: undefined, limit: 20 }, undefined)
  expect(fetch).toHaveBeenNthCalledWith(2, { since_seq: 5, cursor: 'page2', limit: 20 }, undefined)
  expect(batch.lastSeq).toBe(11)
  expect(batch.posts.map((p) => p.id)).toEqual(['p1', 'p2'])
})
it('does not deliver a partial checkpoint when a later page fails', async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce({ items: [post('p1', 8)], last_seq: 8, has_more: true, next_cursor: 'page2' })
    .mockRejectedValueOnce(new Error('Tether unavailable'))
  await expect(drainPostPoll(fetch, 5)).rejects.toThrow('Tether unavailable')
  fetch.mockResolvedValueOnce({ items: [], has_more: false })
  expect((await drainPostPoll(fetch, 5)).lastSeq).toBe(5)
})
it('rejects repeated continuation instead of looping', async () => {
  const fetch = vi.fn().mockResolvedValue({ items: [], has_more: true, next_cursor: 'same' })
  await expect(drainPostPoll(fetch, 0)).rejects.toThrow('pagination cursor')
})
it('merges delivered posts without duplicates and retains sequence order', () => {
  const newer = { ...post('p1', 1), reply_count: 2 }
  expect(mergePosts([post('p2', 2), post('p1', 1)], [newer, post('p3', 3)])).toEqual([
    newer,
    post('p2', 2),
    post('p3', 3),
  ])
})
