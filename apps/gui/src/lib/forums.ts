export interface Forum {
  id: string
  namespace: string
  title: string
}
export interface ForumTopic {
  id: string
  forum_id: string
  title: string
  status: string
  created_by: { id: string; kind: string }
  post_count: number
  created_at: string
  last_activity_at: string
  last_seq: number
}
export interface ForumPost {
  id: string
  topic_id: string
  forum_id: string
  parent_post_id?: string
  seq: number
  depth: number
  reply_count: number
  body_chars: number
  snippet?: string
  body_md?: string
  truncated: boolean
  created_at: string
  topic_title?: string
  provenance: {
    id: string
    kind: string
    verified: boolean
    session_id?: string
    agent_urn?: string
    workstream_id?: string
    project_id?: string
  }
}
export interface ForumPage<T> {
  items: T[]
  has_more: boolean
  next_cursor?: string
  last_seq?: number
  next?: string
}
export type ForumOptions = Record<string, string | number | boolean | undefined>

export function mergePosts(existing: ForumPost[], incoming: ForumPost[]): ForumPost[] {
  const byId = new Map(existing.map((post) => [post.id, post]))
  for (const post of incoming) byId.set(post.id, post)
  return [...byId.values()].sort((a, b) => a.seq - b.seq)
}

// A polling batch is committed only after every cursor page has arrived.
// last_seq is the last delivered post, never the topic/forum's global sequence.
export async function drainPostPoll(
  fetchPage: (opts: ForumOptions, signal?: AbortSignal) => Promise<ForumPage<ForumPost>>,
  since: number,
  signal?: AbortSignal,
): Promise<{ posts: ForumPost[]; lastSeq: number }> {
  let cursor: string | undefined
  let lastSeq = since
  const posts: ForumPost[] = []
  const cursors = new Set<string>()
  do {
    const page = await fetchPage({ since_seq: since, cursor, limit: 20 }, signal)
    posts.push(...(page.items ?? []))
    lastSeq = Math.max(lastSeq, page.last_seq ?? 0)
    if (!page.has_more) break
    if (!page.next_cursor || cursors.has(page.next_cursor))
      throw new Error('Forums returned an invalid pagination cursor.')
    cursor = page.next_cursor
    cursors.add(cursor)
  } while (!signal?.aborted)
  if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
  return { posts, lastSeq }
}
