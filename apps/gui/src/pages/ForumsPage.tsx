import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import Markdown from 'react-markdown'
import { ChevronRight, MessageSquare, MessagesSquare, Plus, SlidersHorizontal } from 'lucide-react'
import { Button, EmptyState, Input, PageHeader, Skeleton, Textarea } from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { ForumCreateDialog } from '@/components/domain/forum-create-dialog'
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
    <div
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 border-b border-red-900/40 bg-red-950/10 px-4 py-3 text-xs text-red-300"
    >
      <p>{error}</p>
      {retry && (
        <Button variant="outline" onClick={retry}>
          Retry
        </Button>
      )}
    </div>
  )
}
const sectionHeading =
  'border-b border-zinc-800/80 bg-zinc-900/30 px-4 py-2.5 text-[10px] font-medium uppercase tracking-[.18em] text-zinc-500'
const fieldClass = 'h-8 rounded-none border-zinc-800 bg-zinc-950 text-xs [color-scheme:dark]'
const selectClass = `${fieldClass} w-full border px-2 text-zinc-300`
const topicColumns =
  'grid grid-cols-[minmax(0,1fr)_4rem] gap-x-4 sm:grid-cols-[minmax(0,1fr)_5rem_10rem]'
const rowLink =
  'transition-colors hover:bg-zinc-900/50 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary'

function DateLabel({ value }: { value: string }) {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return <span>—</span>
  return (
    <time dateTime={value} title={date.toLocaleString()}>
      {date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}
    </time>
  )
}
function Status({ value }: { value: string }) {
  return (
    <span className="rounded border border-zinc-700/70 bg-zinc-900 px-1.5 py-0.5 text-[10px] font-normal uppercase tracking-wider text-zinc-400">
      {value}
    </span>
  )
}
function LoadingRows({ label = 'Loading forums…' }: { label?: string }) {
  return (
    <div role="status" aria-label={label} className="divide-y divide-zinc-800/60">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex h-20 items-center gap-4 px-4">
          <Skeleton className="h-8 w-8 shrink-0 rounded-none" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3 w-1/3 rounded-none" />
            <Skeleton className="h-2 w-1/2 rounded-none" />
          </div>
        </div>
      ))}
    </div>
  )
}
function More({
  page,
  busy,
  onClick,
  noun = 'items',
}: {
  page: ForumPage<unknown>
  busy: boolean
  onClick: () => void
  noun?: string
}) {
  if (!page.items.length) return null
  return (
    <div className="flex min-h-12 items-center justify-between gap-3 border-b border-zinc-800/80 px-4 py-2 text-xs text-zinc-500">
      <span>
        {page.items.length} {noun} loaded
      </span>
      {page.has_more && (
        <Button size="sm" variant="outline" disabled={busy} onClick={onClick}>
          {busy ? 'Loading…' : 'Load more'}
        </Button>
      )}
    </div>
  )
}
function ForumBreadcrumb({
  forumId,
  title,
  thread = false,
}: {
  forumId?: string
  title?: string
  thread?: boolean
}) {
  return (
    <nav
      aria-label="Forum navigation"
      className="flex min-h-10 items-center gap-2 border-b border-zinc-800/80 px-4 py-2 text-xs text-zinc-500"
    >
      <Link to="/forums" className="shrink-0 hover:text-zinc-100">
        All forums
      </Link>
      {forumId && (
        <>
          <ChevronRight className="h-3 w-3 shrink-0" aria-hidden="true" />
          {thread ? (
            <Link
              to={`/forums/${encodeURIComponent(forumId)}`}
              className="truncate hover:text-zinc-100"
            >
              {title || 'Topics'}
            </Link>
          ) : (
            <span aria-current="page" className="truncate text-zinc-300">
              {title || 'Topics'}
            </span>
          )}
        </>
      )}
      {thread && (
        <>
          <ChevronRight className="h-3 w-3 shrink-0" aria-hidden="true" />
          <span aria-current="page">Thread</span>
        </>
      )}
    </nav>
  )
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
    <div className="flex min-h-full min-w-0 flex-col">
      {(!enabled || error || forumId) && <PageHeader title="Forums" />}
      {error ? (
        <ErrorNotice error={error} retry={() => setAttempt((n) => n + 1)} />
      ) : enabled === null ? (
        <LoadingRows />
      ) : !enabled ? (
        <p className="px-4 py-8 text-sm text-zinc-400">
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
      setPage((old) => ({
        ...out,
        items: [
          ...new Map(
            [...old.items, ...(out.items ?? [])].map((forum) => [forum.id, forum]),
          ).values(),
        ],
      }))
      setError('')
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <PageHeader title="Forums">
        <ForumCreateDialog
          disabled={busy && !page.items.length}
          onCreated={(forum) => {
            setPage((old) => ({
              ...old,
              items: [forum, ...old.items.filter((item) => item.id !== forum.id)],
            }))
          }}
        />
      </PageHeader>
      <section aria-label="Forum directory">
        <div className={sectionHeading}>Discussion forums</div>
        {error && (
          <ErrorNotice
            error={error}
            retry={() => {
              if (page.items.length) void more()
              else {
                setBusy(true)
                setAttempt((n) => n + 1)
              }
            }}
          />
        )}
        {busy && !page.items.length && <LoadingRows />}
        <div className="divide-y divide-zinc-800/60 border-b border-zinc-800/80">
          {page.items.map((forum) => (
            <Link
              key={forum.id}
              to={`/forums/${encodeURIComponent(forum.id)}`}
              className={`flex min-h-20 items-center gap-4 px-4 py-4 ${rowLink}`}
            >
              <MessagesSquare className="h-5 w-5 shrink-0 text-zinc-500" aria-hidden="true" />
              <div className="min-w-0 flex-1">
                <h2 className="text-[13px] font-medium text-zinc-100">{forum.title}</h2>
                <p className="mt-1 break-all font-mono text-[11px] text-zinc-500">
                  {forum.namespace}
                </p>
              </div>
              <span className="hidden text-xs text-zinc-500 sm:block">View topics</span>
              <ChevronRight className="h-4 w-4 shrink-0 text-zinc-600" aria-hidden="true" />
            </Link>
          ))}
        </div>
        {!busy && !error && !page.items.length && (
          <EmptyState
            variant="empty"
            title="No forums available"
            description="No forums are available to this operator."
          />
        )}
        <More page={page} busy={busy} onClick={() => void more()} noun="forums" />
      </section>
    </>
  )
}

function ForumBrowse({ forumId }: { forumId: string }) {
  const api = useApi()
  const navigate = useNavigate()
  const [forum, setForum] = useState<Forum | null>(null)
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [appliedFilters, setAppliedFilters] = useState<Record<string, string>>({})
  const [showCreate, setShowCreate] = useState(false)
  const [forumAttempt, setForumAttempt] = useState(0)
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
      .then((out) => {
        setForum(out.forum)
        setForumError('')
      })
      .catch((err) => {
        if (!isAborted(err)) setForumError(message(err))
      })
    return () => c.abort()
  }, [api, forumId, forumAttempt])
  function filter(key: string, value: string) {
    setFilters((old) => ({ ...old, [key]: value }))
  }
  const opts: ForumOptions = Object.fromEntries(
    Object.entries(appliedFilters)
      .filter(([, value]) => value)
      .map(([key, value]) => [
        key,
        (key === 'created_after' || key === 'created_before') &&
        !Number.isNaN(new Date(value).getTime())
          ? new Date(value).toISOString()
          : value,
      ]),
  )
  async function create() {
    if (creating || !title.trim()) return
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
    <section>
      <ForumBreadcrumb forumId={forumId} title={forum?.title} />
      <div className="flex items-center justify-between gap-4 border-b border-zinc-800/80 px-4 py-4">
        <div className="min-w-0">
          <h2 className="break-words text-base font-semibold text-zinc-100">
            {forum?.title ?? 'Topics'}
          </h2>
          <p className="mt-1 text-xs text-zinc-500">Browse discussions or start a new topic.</p>
        </div>
        <Button
          size="sm"
          className="shrink-0"
          disabled={creating}
          onClick={() => setShowCreate((v) => !v)}
          aria-expanded={showCreate}
          aria-controls="new-topic-form"
        >
          <Plus className="h-3.5 w-3.5" />
          New topic
        </Button>
      </div>
      {forumError && <ErrorNotice error={forumError} retry={() => setForumAttempt((n) => n + 1)} />}
      {showCreate && (
        <form
          id="new-topic-form"
          className="space-y-3 border-b border-zinc-800/80 bg-zinc-900/20 px-4 py-4"
          onSubmit={(e) => {
            e.preventDefault()
            void create()
          }}
        >
          <label htmlFor="new-topic-title" className="block text-xs font-medium text-zinc-300">
            Topic title
          </label>
          <Input
            id="new-topic-title"
            className={fieldClass}
            aria-label="New topic title"
            placeholder="What would you like to discuss?"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={500}
            disabled={creating}
            autoFocus
          />
          <div className="flex gap-2">
            <Button size="sm" disabled={creating || !title.trim()} type="submit">
              {creating ? 'Creating…' : 'Create topic'}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              type="button"
              disabled={creating}
              onClick={() => setShowCreate(false)}
            >
              Cancel
            </Button>
          </div>
          {writeError && <ErrorNotice error={writeError} />}
        </form>
      )}
      <form
        className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(search.trim())
        }}
      >
        <Input
          className={`${fieldClass} min-w-40 flex-1 sm:max-w-sm`}
          aria-label="Search within forum"
          placeholder="Search within this forum"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Button size="sm" variant="outline" type="submit">
          Search
        </Button>
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
      <details className="border-b border-zinc-800/80">
        <summary className="flex cursor-pointer items-center gap-2 px-4 py-2.5 text-xs text-zinc-400 hover:text-zinc-200">
          <SlidersHorizontal className="h-3.5 w-3.5" />
          Filters
          {Object.values(appliedFilters).filter(Boolean).length > 0 && (
            <Status value={`${Object.values(appliedFilters).filter(Boolean).length} active`} />
          )}
        </summary>
        <form
          className="grid gap-3 px-4 pb-4 sm:grid-cols-3 lg:grid-cols-4"
          onSubmit={(e) => {
            e.preventDefault()
            setAppliedFilters(filters)
          }}
        >
          {[
            ['status', 'Status', 'Status (e.g. open)'],
            ['author', 'Author', 'Author principal'],
            ['workstream_id', 'Workstream', 'Workstream ID'],
            ['project_id', 'Project', 'Project ID'],
          ].map(([key, label, placeholder]) => (
            <label key={key} className="flex flex-col gap-1.5 text-[11px] text-zinc-500">
              {label}
              <Input
                className={fieldClass}
                aria-label={`${label} filter`}
                placeholder={placeholder}
                value={filters[key] ?? ''}
                onChange={(e) => filter(key, e.target.value)}
              />
            </label>
          ))}
          <label className="flex flex-col gap-1.5 text-[11px] text-zinc-500">
            Author kind
            <select
              aria-label="Author kind filter"
              className={selectClass}
              value={filters.author_kind ?? ''}
              onChange={(e) => filter('author_kind', e.target.value)}
            >
              <option value="">All authors</option>
              <option value="user">Human</option>
              <option value="agent">Agent</option>
            </select>
          </label>
          <label className="flex flex-col gap-1.5 text-[11px] text-zinc-500">
            Replies
            <select
              aria-label="Replies filter"
              className={selectClass}
              value={filters.has_replies ?? ''}
              onChange={(e) => filter('has_replies', e.target.value)}
            >
              <option value="">Any replies</option>
              <option value="true">Has replies</option>
              <option value="false">No replies</option>
            </select>
          </label>
          {['after', 'before'].map((bound) => (
            <label key={bound} className="flex flex-col gap-1.5 text-[11px] text-zinc-500">
              Created {bound}
              <Input
                className={fieldClass}
                aria-label={`Created ${bound}`}
                type="datetime-local"
                value={filters[`created_${bound}`] ?? ''}
                onChange={(e) => filter(`created_${bound}`, e.target.value)}
              />
            </label>
          ))}
          <div className="flex items-end gap-2 sm:col-span-3 lg:col-span-4">
            <Button size="sm" type="submit" variant="outline">
              Apply filters
            </Button>
            <Button
              size="sm"
              type="button"
              variant="ghost"
              onClick={() => {
                setFilters({})
                setAppliedFilters({})
              }}
            >
              Reset filters
            </Button>
          </div>
        </form>
      </details>
      <BrowseResults
        key={JSON.stringify([query, opts])}
        forumId={forumId}
        query={query}
        opts={opts}
      />
    </section>
  )
}

function BrowseResults({
  forumId,
  query,
  opts,
}: {
  forumId: string
  query: string
  opts: ForumOptions
}) {
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
    <div aria-label={query ? 'Search results' : 'Topics'}>
      <div className={`${sectionHeading} ${query ? '' : topicColumns}`}>
        <span>{query ? `Posts matching “${query}”` : 'Topic'}</span>
        {!query && (
          <>
            <span className="text-right">Posts</span>
            <span className="hidden sm:block">Last activity</span>
          </>
        )}
      </div>
      {error && (
        <ErrorNotice
          error={error}
          retry={() => {
            if (page.items.length) void more()
            else {
              setBusy(true)
              setAttempt((n) => n + 1)
            }
          }}
        />
      )}
      {busy && !page.items.length && (
        <LoadingRows label={query ? 'Searching posts…' : 'Loading topics…'} />
      )}
      <div className="divide-y divide-zinc-800/60 border-b border-zinc-800/80">
        {page.items.map((item) =>
          'topic_id' in item ? (
            <Link
              key={item.id}
              className={`block px-4 py-4 ${rowLink}`}
              to={`/forums/${encodeURIComponent(forumId)}/topics/${encodeURIComponent(item.topic_id)}`}
            >
              <p className="text-[13px] font-medium text-zinc-100">
                {item.topic_title ?? 'Open topic'}
              </p>
              <p className="mt-1 text-[11px] text-zinc-500">
                {item.provenance.id} · <DateLabel value={item.created_at} />
              </p>
              <p className="mt-2 whitespace-pre-wrap break-words text-[13px] leading-6 text-zinc-300">
                {item.snippet}
              </p>
            </Link>
          ) : (
            <Link
              key={item.id}
              className={`${topicColumns} items-center px-4 py-4 ${rowLink}`}
              to={`/forums/${encodeURIComponent(forumId)}/topics/${encodeURIComponent(item.id)}`}
            >
              <div className="flex min-w-0 items-center gap-3">
                <MessageSquare
                  className="hidden h-4 w-4 shrink-0 text-zinc-600 sm:block"
                  aria-hidden="true"
                />
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="break-words text-[13px] font-medium text-zinc-100">
                      {item.title}
                    </h3>
                    <Status value={item.status} />
                  </div>
                  <p className="mt-1 break-words text-[11px] text-zinc-500">
                    {item.created_by.id} · <DateLabel value={item.created_at} />
                  </p>
                  <p className="mt-1 text-[11px] text-zinc-500 sm:hidden">
                    Active <DateLabel value={item.last_activity_at || item.created_at} />
                  </p>
                </div>
              </div>
              <span className="text-right font-mono text-xs tabular-nums text-zinc-400">
                {item.post_count}
              </span>
              <span className="hidden text-xs text-zinc-400 sm:block">
                <DateLabel value={item.last_activity_at || item.created_at} />
              </span>
            </Link>
          ),
        )}
      </div>
      {!busy && !error && !page.items.length && (
        <EmptyState
          variant="empty"
          title={query ? 'No matching posts' : 'No topics'}
          description={
            query
              ? 'Try another search or adjust your filters.'
              : 'Start a new topic to begin a discussion, or adjust your filters.'
          }
        />
      )}
      <More
        page={page}
        busy={busy}
        onClick={() => void more()}
        noun={query ? 'matching posts' : 'topics'}
      />
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
  const composer = useRef<HTMLTextAreaElement>(null)
  const sequence = useRef(0)
  const initialized = useRef(false)
  useEffect(() => {
    const c = new AbortController()
    initialized.current = false
    sequence.current = 0
    api
      .forumTopic(topicId, { limit: 20 }, c.signal)
      .then((out) => {
        if (out.topic.forum_id !== forumId)
          throw new Error('This topic belongs to a different forum.')
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
    if (sending || !body.trim()) return
    setSending(true)
    setWriteError('')
    try {
      const out = await api.forumCreatePost(topicId, body, replyTo?.id)
      setPage((old) => ({
        ...old,
        items: mergePosts(
          old.items.map((p) =>
            p.id === replyTo?.id ? { ...p, reply_count: p.reply_count + 1 } : p,
          ),
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
  const postsById = new Map(page.items.map((post) => [post.id, post]))
  function focusComposer(post: ForumPost | null) {
    setReplyTo(post)
    composer.current?.focus()
    composer.current?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
  }
  return (
    <section>
      <ForumBreadcrumb forumId={forumId} thread />
      <div className="flex items-center justify-between gap-4 border-b border-zinc-800/80 px-4 py-4">
        <div className="min-w-0">
          <h2 className="break-words text-base font-semibold text-zinc-100">
            {topic?.title ?? 'Topic'}
          </h2>
          {topic && (
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-zinc-500">
              <Status value={topic.status} />
              <span>
                Started by {topic.created_by.id} · <DateLabel value={topic.created_at} />
              </span>
            </div>
          )}
        </div>
        {topic && (
          <Button
            size="sm"
            className="shrink-0"
            disabled={sending}
            onClick={() => focusComposer(null)}
          >
            Reply to topic
          </Button>
        )}
      </div>
      {error && (
        <ErrorNotice
          error={error}
          retry={() => {
            setBusy(true)
            setAttempt((n) => n + 1)
          }}
        />
      )}
      {busy && !page.items.length && <LoadingRows label="Loading thread…" />}
      {page.items.map((post) => (
        <PostRow
          key={post.id}
          post={post}
          parent={post.parent_post_id ? postsById.get(post.parent_post_id) : undefined}
          reply={() => focusComposer(post)}
          replyingDisabled={sending}
        />
      ))}
      {!busy && !error && !page.items.length && (
        <EmptyState
          variant="empty"
          title="No posts yet"
          description="Write the first post to start this discussion."
        />
      )}
      <More page={page} busy={busy} onClick={() => void more()} noun="posts" />
      {topic && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void send()
          }}
        >
          <div className={sectionHeading}>Join the discussion</div>
          <div className="space-y-3 px-4 py-4 sm:pl-48">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <label htmlFor="forum-body" className="text-xs font-medium text-zinc-300">
                {replyTo ? `Reply to ${replyTo.provenance.id}` : 'New post'}
              </label>
              {replyTo && (
                <Button
                  size="sm"
                  type="button"
                  variant="ghost"
                  disabled={sending}
                  onClick={() => setReplyTo(null)}
                >
                  Cancel reply
                </Button>
              )}
            </div>
            {replyTo && (
              <p className="border-l-2 border-zinc-700 pl-3 text-xs text-zinc-500 line-clamp-3">
                {replyTo.snippet ?? replyTo.body_md}
              </p>
            )}
            <Textarea
              ref={composer}
              id="forum-body"
              aria-label="Post Markdown"
              className="min-h-32 rounded-none border-zinc-800 bg-zinc-950 text-[13px]"
              placeholder="Write a post…"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={5}
              disabled={sending}
            />
            <div className="flex items-center justify-between gap-3">
              <span className="text-[11px] text-zinc-500">Markdown supported</span>
              <Button size="sm" type="submit" disabled={sending || !body.trim()}>
                {sending ? 'Posting…' : replyTo ? 'Post reply' : 'Post'}
              </Button>
            </div>
            {writeError && <ErrorNotice error={writeError} />}
          </div>
        </form>
      )}
    </section>
  )
}

function PostRow({
  post,
  parent,
  reply,
  replyingDisabled,
}: {
  post: ForumPost
  parent?: ForumPost
  reply: () => void
  replyingDisabled: boolean
}) {
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
        err instanceof ApiError && err.status === 503
          ? `Forums unavailable. ${message(err)}`
          : message(err),
      )
    } finally {
      setBusy(false)
    }
  }
  const visiblePost = expanded && full ? full : post
  const canExpand =
    post.truncated || (!post.body_md && post.body_chars > (post.snippet?.length ?? 0))
  return (
    <article
      id={`post-${post.id}`}
      className="grid min-w-0 border-b border-zinc-800/80 sm:grid-cols-[11rem_minmax(0,1fr)]"
    >
      <aside className="flex min-w-0 items-center gap-3 border-b border-zinc-800/60 bg-zinc-900/20 px-4 py-3 sm:flex-col sm:items-start sm:gap-2 sm:border-r sm:border-b-0 sm:py-5">
        <div
          aria-hidden="true"
          className="flex h-8 w-8 shrink-0 items-center justify-center border border-zinc-700 bg-zinc-800 text-sm font-medium text-zinc-300 sm:h-10 sm:w-10"
        >
          {post.provenance.id.charAt(0).toUpperCase()}
        </div>
        <div className="min-w-0">
          <p className="break-words text-xs font-medium text-zinc-200">{post.provenance.id}</p>
          <p className="mt-1 text-[10px] uppercase tracking-wider text-zinc-500">
            {post.provenance.kind === 'user' ? 'Human' : post.provenance.kind}
          </p>
          {post.provenance.verified && (
            <p className="mt-1 text-[10px] text-zinc-500">Authenticated</p>
          )}
        </div>
      </aside>
      <div className="min-w-0 px-4 py-4">
        <div className="mb-4 flex items-center justify-between gap-3 text-[11px] text-zinc-500">
          <DateLabel value={post.created_at} />
          <a
            href={`#post-${post.id}`}
            className="font-mono hover:text-zinc-200"
            aria-label={`Link to post ${post.seq}`}
          >
            #{post.seq}
          </a>
        </div>
        {post.parent_post_id && (
          <p className="mb-3 border-l-2 border-zinc-700 pl-3 text-xs text-zinc-500">
            {parent ? (
              <a href={`#post-${parent.id}`} className="hover:text-zinc-200">
                Reply to {parent.provenance.id} · #{parent.seq}
              </a>
            ) : (
              'In reply to an earlier post'
            )}
          </p>
        )}
        <div className="space-y-3 break-words text-[13px] leading-6 text-zinc-300 [&_a]:text-primary [&_a]:underline [&_pre]:max-w-full [&_pre]:overflow-auto [&_pre]:bg-zinc-900 [&_pre]:p-3 [&_h1]:text-lg [&_h2]:text-base [&_h3]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_blockquote]:border-l-2 [&_blockquote]:border-zinc-700 [&_blockquote]:pl-3 [&_img]:max-w-full">
          <Markdown>
            {visiblePost.body_md ?? visiblePost.snippet ?? 'Expand to read this post.'}
          </Markdown>
          {expanded && full?.truncated && (
            <p className="text-xs text-zinc-500">Body truncated ({full.body_chars} characters).</p>
          )}
        </div>
        <div className="mt-4 flex items-center gap-2">
          {canExpand && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void expand()}>
              {expanded ? 'Collapse' : busy ? 'Loading…' : 'Expand body'}
            </Button>
          )}
          <Button size="sm" variant="ghost" disabled={replyingDisabled} onClick={reply}>
            Reply
          </Button>
          {post.reply_count > 0 && (
            <span className="ml-auto text-[11px] text-zinc-500">
              {post.reply_count} {post.reply_count === 1 ? 'reply' : 'replies'}
            </span>
          )}
        </div>
        {error && <ErrorNotice error={error} retry={() => void expand()} />}
      </div>
    </article>
  )
}
