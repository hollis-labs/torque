import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import Markdown from 'react-markdown'
import { Button, Input, Textarea } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { ApiError } from '@/lib/api'
import { drainPostPoll, mergePosts } from '@/lib/forums'
import type { Forum, ForumTopic, ForumPost, ForumPage, ForumOptions } from '@/lib/forums'

const emptyPage = <T,>(): ForumPage<T> => ({ items: [], has_more: false })
function message(err: unknown): string {
  return err instanceof Error ? err.message : 'Forums request failed.'
}
function isAborted(err: unknown): boolean {
  return err instanceof Error && err.name === 'AbortError'
}
function ErrorNotice({ error, retry }: { error: string; retry?: () => void }) {
  return (
    <div role="alert" className="rounded border border-border p-4 text-sm">
      <p>{error}</p>
      {retry && (
        <Button variant="outline" onClick={retry}>
          Retry
        </Button>
      )}
    </div>
  )
}
function More({ page, busy, onClick }: { page: ForumPage<unknown>; busy: boolean; onClick: () => void }) {
  return page.has_more ? (
    <Button variant="outline" disabled={busy} onClick={onClick}>
      Load more
    </Button>
  ) : null
}

export default function ForumsPage() {
  const api = useApi()
  const { forumId, topicId } = useParams()
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    api
      .getFeatureFlags()
      .then((flags) => {
        if (live) {
          setEnabled(!!flags.forums)
          setError('')
        }
      })
      .catch((err) => {
        if (live) setError(message(err))
      })
    return () => {
      live = false
    }
  }, [api, attempt])
  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Forums</h1>
        <Link to="/forums" className="text-sm text-primary">
          All forums
        </Link>
      </header>
      {error ? (
        <ErrorNotice error={error} retry={() => setAttempt((n) => n + 1)} />
      ) : enabled === null ? (
        <p>Loading forums…</p>
      ) : !enabled ? (
        <p>
          Forums is disabled. Enable it in{' '}
          <Link to="/settings" className="text-primary underline">
            Settings
          </Link>
          .
        </p>
      ) : topicId && forumId ? (
        <Thread key={`${forumId}:${topicId}`} forumId={forumId} topicId={topicId} />
      ) : forumId ? (
        <ForumBrowse key={forumId} forumId={forumId} />
      ) : (
        <ForumPicker />
      )}
    </div>
  )
}

function ForumPicker() {
  const api = useApi()
  const [page, setPage] = useState(emptyPage<Forum>)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    api
      .forumsList({ limit: 20 }, controller.signal)
      .then((out) => {
        setPage({ ...out, items: out.items ?? [] })
        setError('')
      })
      .catch((err) => {
        if (!isAborted(err)) setError(message(err))
      })
      .finally(() => {
        if (!controller.signal.aborted) setBusy(false)
      })
    return () => controller.abort()
  }, [api, attempt])
  async function more() {
    setBusy(true)
    try {
      const out = await api.forumsList({ cursor: page.next_cursor, limit: 20 })
      setPage({ ...out, items: [...page.items, ...(out.items ?? [])] })
      setError('')
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="space-y-3">
      {error && (
        <ErrorNotice
          error={error}
          retry={() => {
            setBusy(true)
            setAttempt((n) => n + 1)
          }}
        />
      )}
      {busy && <p>Loading…</p>}
      {page.items.map((forum) => (
        <Link
          className="block rounded border border-border p-4 hover:bg-accent"
          key={forum.id}
          to={`/forums/${encodeURIComponent(forum.id)}`}
        >
          <h2 className="font-medium">{forum.title}</h2>
          <p className="text-xs text-muted-foreground">{forum.namespace}</p>
        </Link>
      ))}
      {!busy && !error && !page.items.length && <p>No forums are available to this operator.</p>}
      <More page={page} busy={busy} onClick={() => void more()} />
    </section>
  )
}

function ForumBrowse({ forumId }: { forumId: string }) {
  const api = useApi()
  const navigate = useNavigate()
  const [forum, setForum] = useState<Forum | null>(null)
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [title, setTitle] = useState('')
  const [creating, setCreating] = useState(false)
  const [writeError, setWriteError] = useState('')
  const [forumError, setForumError] = useState('')
  useEffect(() => {
    const c = new AbortController()
    api
      .forumGet(forumId, c.signal)
      .then((out) => setForum(out.forum))
      .catch((err) => {
        if (!isAborted(err)) setForumError(message(err))
      })
    return () => c.abort()
  }, [api, forumId])
  function filter(key: string, value: string) {
    setFilters((old) => ({ ...old, [key]: value }))
  }
  const opts: ForumOptions = Object.fromEntries(
    Object.entries(filters)
      .filter(([, value]) => value)
      .map(([key, value]) => [
        key,
        key === 'created_after' || key === 'created_before' ? new Date(value).toISOString() : value,
      ]),
  )
  async function create() {
    setCreating(true)
    setWriteError('')
    try {
      const out = await api.forumCreateTopic(forumId, title.trim())
      navigate(`/forums/${encodeURIComponent(forumId)}/topics/${encodeURIComponent(out.topic.id)}`)
    } catch (err) {
      setWriteError(message(err))
    } finally {
      setCreating(false)
    }
  }
  return (
    <section className="space-y-4">
      <h2 className="font-semibold">{forum?.title ?? forumId}</h2>
      {forumError && <ErrorNotice error={forumError} />}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(search.trim())
        }}
      >
        <Input
          aria-label="Search within forum"
          placeholder="Search within this forum"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Button type="submit">Search</Button>
        {query && (
          <Button
            variant="outline"
            type="button"
            onClick={() => {
              setSearch('')
              setQuery('')
            }}
          >
            Clear search
          </Button>
        )}
      </form>
      <div className="grid gap-2 sm:grid-cols-3">
        <Input
          aria-label="Status filter"
          placeholder="Status (e.g. open)"
          value={filters.status ?? ''}
          onChange={(e) => filter('status', e.target.value)}
        />
        <Input
          aria-label="Author filter"
          placeholder="Author principal"
          value={filters.author ?? ''}
          onChange={(e) => filter('author', e.target.value)}
        />
        <select
          aria-label="Author kind filter"
          className="rounded border border-border bg-background px-3 py-2 text-sm"
          value={filters.author_kind ?? ''}
          onChange={(e) => filter('author_kind', e.target.value)}
        >
          <option value="">All authors</option>
          <option value="user">Human</option>
          <option value="agent">Agent</option>
        </select>
        <label className="text-xs">
          Created after
          <Input
            aria-label="Created after"
            type="datetime-local"
            value={filters.created_after ?? ''}
            onChange={(e) => filter('created_after', e.target.value)}
          />
        </label>
        <label className="text-xs">
          Created before
          <Input
            aria-label="Created before"
            type="datetime-local"
            value={filters.created_before ?? ''}
            onChange={(e) => filter('created_before', e.target.value)}
          />
        </label>
        <select
          aria-label="Replies filter"
          className="rounded border border-border bg-background px-3 py-2 text-sm"
          value={filters.has_replies ?? ''}
          onChange={(e) => filter('has_replies', e.target.value)}
        >
          <option value="">Any replies</option>
          <option value="true">Has replies</option>
          <option value="false">No replies</option>
        </select>
        <Input
          aria-label="Workstream filter"
          placeholder="Workstream ID"
          value={filters.workstream_id ?? ''}
          onChange={(e) => filter('workstream_id', e.target.value)}
        />
        <Input
          aria-label="Project filter"
          placeholder="Project ID"
          value={filters.project_id ?? ''}
          onChange={(e) => filter('project_id', e.target.value)}
        />
      </div>
      <BrowseResults key={JSON.stringify([query, opts])} forumId={forumId} query={query} opts={opts} />
      <form
        className="flex gap-2 border-t border-border pt-4"
        onSubmit={(e) => {
          e.preventDefault()
          void create()
        }}
      >
        <Input
          aria-label="New topic title"
          placeholder="New topic title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={500}
        />
        <Button disabled={creating || !title.trim()} type="submit">
          Create topic
        </Button>
      </form>
      {writeError && <ErrorNotice error={writeError} />}
    </section>
  )
}

function BrowseResults({ forumId, query, opts }: { forumId: string; query: string; opts: ForumOptions }) {
  const api = useApi()
  const [page, setPage] = useState(emptyPage<ForumTopic | ForumPost>)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const optsRef = useRef(opts)
  function load(cursor?: string, signal?: AbortSignal) {
    const options = { ...optsRef.current, cursor, limit: 20 }
    return query
      ? api.forumSearch(forumId, query, options, signal)
      : api.forumTopics(forumId, options, signal)
  }
  useEffect(() => {
    const c = new AbortController()
    const options = { ...optsRef.current, limit: 20 }
    const pending = query
      ? api.forumSearch(forumId, query, options, c.signal)
      : api.forumTopics(forumId, options, c.signal)
    pending
      .then((out) => {
        setPage({ ...out, items: out.items ?? [] })
        setError('')
      })
      .catch((err) => {
        if (!isAborted(err)) setError(message(err))
      })
      .finally(() => {
        if (!c.signal.aborted) setBusy(false)
      })
    return () => c.abort()
  }, [api, forumId, query, attempt])
  async function more() {
    setBusy(true)
    try {
      const out = await load(page.next_cursor)
      setPage((old) => ({ ...out, items: [...old.items, ...(out.items ?? [])] }))
      setError('')
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-2">
      {error && (
        <ErrorNotice
          error={error}
          retry={() => {
            setBusy(true)
            setAttempt((n) => n + 1)
          }}
        />
      )}
      {page.items.map((item) =>
        'topic_id' in item ? (
          <Link
            key={item.id}
            className="block rounded border border-border p-3"
            to={`/forums/${encodeURIComponent(forumId)}/topics/${encodeURIComponent(item.topic_id)}`}
          >
            <p className="font-medium">{item.topic_title ?? 'Open topic'}</p>
            <p className="text-xs text-muted-foreground">
              {item.provenance.id} · {item.provenance.kind} · {item.created_at}
            </p>
            <p className="mt-1 text-sm whitespace-pre-wrap">{item.snippet}</p>
          </Link>
        ) : (
          <Link
            key={item.id}
            className="block rounded border border-border p-3"
            to={`/forums/${encodeURIComponent(forumId)}/topics/${encodeURIComponent(item.id)}`}
          >
            <p className="font-medium">{item.title}</p>
            <p className="text-xs text-muted-foreground">
              {item.status} · {item.created_by.id} · {item.post_count} posts ·{' '}
              {item.last_activity_at || item.created_at}
            </p>
          </Link>
        ),
      )}
      {busy && <p className="text-sm">Loading…</p>}
      {!busy && !error && !page.items.length && <p>No {query ? 'matching posts' : 'topics'}.</p>}
      <More page={page} busy={busy} onClick={() => void more()} />
    </div>
  )
}

function Thread({ forumId, topicId }: { forumId: string; topicId: string }) {
  const api = useApi()
  const [topic, setTopic] = useState<ForumTopic | null>(null)
  const [page, setPage] = useState(emptyPage<ForumPost>)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [replyTo, setReplyTo] = useState<ForumPost | null>(null)
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const [writeError, setWriteError] = useState('')
  const sequence = useRef(0)
  const initialized = useRef(false)
  useEffect(() => {
    const c = new AbortController()
    initialized.current = false
    sequence.current = 0
    api
      .forumTopic(topicId, { limit: 20 }, c.signal)
      .then((out) => {
        if (out.topic.forum_id !== forumId) throw new Error('This topic belongs to a different forum.')
        setTopic(out.topic)
        setPage({ ...out.outline, items: out.outline.items ?? [] })
        sequence.current = out.outline.last_seq ?? 0
        initialized.current = true
        setError('')
      })
      .catch((err) => {
        if (!isAborted(err)) setError(message(err))
      })
      .finally(() => {
        if (!c.signal.aborted) setBusy(false)
      })
    return () => c.abort()
  }, [api, topicId, forumId, attempt])
  // Pause polling while the initial outline still has undiscovered pages.
  useEffect(() => {
    if (!initialized.current || page.has_more || busy) return
    const c = new AbortController()
    let polling = false
    const timer = window.setInterval(async () => {
      if (polling) return
      polling = true
      try {
        const batch = await drainPostPoll(
          (opts, signal) => api.forumPosts(topicId, opts, signal),
          sequence.current,
          c.signal,
        )
        if (!c.signal.aborted) {
          setPage((old) => ({ ...old, items: mergePosts(old.items, batch.posts) }))
          sequence.current = batch.lastSeq
          setError('')
        }
      } catch (err) {
        if (!isAborted(err)) setError(message(err))
      } finally {
        polling = false
      }
    }, 10000)
    return () => {
      window.clearInterval(timer)
      c.abort()
    }
  }, [api, topicId, page.has_more, busy, attempt])
  async function more() {
    setBusy(true)
    try {
      const out = await api.forumPosts(topicId, { cursor: page.next_cursor, limit: 20 })
      setPage((old) => ({ ...out, items: mergePosts(old.items, out.items ?? []) }))
      sequence.current = Math.max(sequence.current, out.last_seq ?? 0)
      setError('')
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }
  async function send() {
    setSending(true)
    setWriteError('')
    try {
      const out = await api.forumCreatePost(topicId, body, replyTo?.id)
      setPage((old) => ({
        ...old,
        items: mergePosts(
          old.items.map((p) => (p.id === replyTo?.id ? { ...p, reply_count: p.reply_count + 1 } : p)),
          [out.post],
        ),
      }))
      // Do not advance sequence here: concurrent unseen posts still need delivery.
      setBody('')
      setReplyTo(null)
    } catch (err) {
      setWriteError(message(err))
    } finally {
      setSending(false)
    }
  }
  return (
    <section className="space-y-4">
      <Link to={`/forums/${encodeURIComponent(forumId)}`} className="text-sm text-primary">
        ← Topics
      </Link>
      <h2 className="font-semibold">{topic?.title ?? 'Topic'}</h2>
      {error && (
        <ErrorNotice
          error={error}
          retry={() => {
            setBusy(true)
            setAttempt((n) => n + 1)
          }}
        />
      )}
      {page.items.map((post) => (
        <PostRow key={post.id} post={post} reply={() => setReplyTo(post)} />
      ))}
      {busy && <p>Loading thread…</p>}
      {!busy && !error && !page.items.length && <p>No posts yet.</p>}
      <More page={page} busy={busy} onClick={() => void more()} />
      {topic && (
        <form
          className="space-y-2 border-t border-border pt-4"
          onSubmit={(e) => {
            e.preventDefault()
            void send()
          }}
        >
          <label htmlFor="forum-body" className="text-sm font-medium">
            {replyTo ? `Reply to ${replyTo.provenance.id}` : 'New post'}
          </label>
          {replyTo && (
            <Button type="button" variant="ghost" onClick={() => setReplyTo(null)}>
              Cancel reply
            </Button>
          )}
          <Textarea
            id="forum-body"
            aria-label="Post Markdown"
            placeholder="Write a Markdown post…"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={5}
          />
          <Button type="submit" disabled={sending || !body.trim()}>
            {sending ? 'Posting…' : replyTo ? 'Post reply' : 'Post'}
          </Button>
          {writeError && <ErrorNotice error={writeError} />}
        </form>
      )}
    </section>
  )
}

function PostRow({ post, reply }: { post: ForumPost; reply: () => void }) {
  const api = useApi()
  const [full, setFull] = useState<ForumPost | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function expand() {
    if (expanded) {
      setExpanded(false)
      return
    }
    if (full) {
      setExpanded(true)
      return
    }
    setBusy(true)
    try {
      const out = await api.forumPost(post.id)
      setFull(out.post)
      setExpanded(true)
      setError('')
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 503 ? `Forums unavailable. ${message(err)}` : message(err),
      )
    } finally {
      setBusy(false)
    }
  }
  return (
    <article
      className="rounded border border-border p-3"
      style={{ marginLeft: `${Math.min(post.depth, 8) * 16}px` }}
    >
      <p className="text-xs text-muted-foreground">
        {post.provenance.id} · {post.provenance.kind}
        {post.provenance.verified ? ' · authenticated' : ''} · {post.created_at} · {post.reply_count} replies
      </p>
      {post.parent_post_id && (
        <p className="text-xs text-muted-foreground">Reply to post {post.parent_post_id}</p>
      )}
      {expanded && full ? (
        <div className="space-y-2 py-2 text-sm [&_a]:text-primary [&_a]:underline [&_pre]:overflow-auto [&_pre]:bg-muted [&_pre]:p-3 [&_h1]:text-lg [&_h2]:text-base [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5">
          <Markdown>{full.body_md ?? full.snippet ?? ''}</Markdown>
          {full.truncated && <p>Body truncated ({full.body_chars} characters).</p>}
        </div>
      ) : (
        <p className="py-2 text-sm whitespace-pre-wrap">
          {post.snippet ?? post.body_md ?? 'Expand to read this post.'}
        </p>
      )}
      <div className="flex gap-2">
        <Button variant="ghost" disabled={busy} onClick={() => void expand()}>
          {expanded ? 'Collapse' : busy ? 'Loading…' : 'Expand body'}
        </Button>
        <Button variant="ghost" onClick={reply}>
          Reply
        </Button>
      </div>
      {error && <ErrorNotice error={error} retry={() => void expand()} />}
    </article>
  )
}
