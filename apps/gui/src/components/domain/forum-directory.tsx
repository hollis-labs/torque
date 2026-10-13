import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Input,
  PageHeader,
  Textarea,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { allForumPages, forumOrder } from '@/lib/forums'
import type { Forum, ForumCategory, ForumActivity } from '@/lib/forums'
import { ForumCreateDialog } from './forum-create-dialog'
import { ForumManagement, forumFieldClass } from './forum-management'

export function ForumDirectory() {
  const api = useApi()
  const [forums, setForums] = useState<Forum[]>([])
  const [categories, setCategories] = useState<ForumCategory[]>([])
  const [activity, setActivity] = useState<Record<string, ForumActivity | null>>({})
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [archived, setArchived] = useState('')
  const refresh = () => {
    setBusy(true)
    setAttempt((n) => n + 1)
  }
  useEffect(() => {
    const c = new AbortController()
    Promise.all([
      allForumPages(
        (opts, signal) => api.forumsList(opts, signal),
        { archived: archived || undefined },
        c.signal,
      ),
      allForumPages((opts, signal) => api.forumCategories(opts, signal), {}, c.signal),
    ])
      .then(([boards, cats]) => {
        if (!c.signal.aborted) {
          setActivity({})
          setForums(boards)
          setCategories(cats)
          setError('')
        }
      })
      .catch((e) => {
        if (!c.signal.aborted)
          setError(e instanceof Error ? e.message : 'Could not load forums.')
      })
      .finally(() => {
        if (!c.signal.aborted) setBusy(false)
      })
    return () => c.abort()
  }, [api, archived, attempt])
  useEffect(() => {
    const c = new AbortController()
    let next = 0
    async function worker() {
      while (!c.signal.aborted && next < forums.length) {
        const id = forums[next++].id
        let value: ForumActivity | null = null
        try {
          value = await api.forumActivity(id, c.signal)
        } catch {
          /* Unavailable is distinct from zero. */
        }
        if (!c.signal.aborted) setActivity((old) => ({ ...old, [id]: value }))
      }
    }
    void Promise.all(Array.from({ length: Math.min(4, forums.length) }, worker))
    return () => c.abort()
  }, [api, forums])
  const columns = 'grid grid-cols-[minmax(10rem,1fr)_5rem_13rem_2rem] items-center gap-3'
  function boardRow(f: Forum, inheritedArchive = false) {
    const stats = activity[f.id]
    const last = stats?.last_post
    return (
      <div key={f.id} className={`${columns} border-b border-zinc-800/60 py-3 pl-7 pr-4`}>
        <Link
          className="min-w-0 hover:text-primary"
          to={`/forums/${encodeURIComponent(f.id)}`}
        >
          <h3 className="text-[13px] font-medium">
            {f.title}
            {(f.archived || inheritedArchive) && (
              <span className="ml-2 text-[10px] uppercase text-zinc-500">
                {f.archived ? 'Archived' : 'Category archived'}
              </span>
            )}
          </h3>
          {f.description && <p className="mt-1 text-xs text-zinc-400">{f.description}</p>}
        </Link>
        <div
          aria-label={`${f.title} post count`}
          className="self-stretch border-l border-zinc-800 px-3 text-center text-xs tabular-nums flex items-center justify-center"
        >
          {stats === undefined
            ? '…'
            : stats === null
              ? '—'
              : stats.post_count.toLocaleString()}
        </div>
        <div className="border-l border-zinc-800 pl-3 text-xs text-zinc-400">
          {last ? (
            <Link
              className="hover:text-primary"
              to={`/forums/${encodeURIComponent(f.id)}/topics/${encodeURIComponent(last.topic_id)}#post-${last.id}`}
            >
              <div className="truncate text-zinc-200">
                {last.provenance.id || last.provenance.kind}
              </div>
              <time className="mt-1 block" dateTime={last.created_at}>
                {new Date(last.created_at).toLocaleString()}
              </time>
            </Link>
          ) : stats === undefined ? (
            'Loading…'
          ) : stats === null ? (
            'Unavailable'
          ) : (
            'No posts yet'
          )}
        </div>
        <ForumManagement
          kind="forum"
          item={f}
          readOnly={f.archived || inheritedArchive}
          onChanged={refresh}
        />
      </div>
    )
  }
  const ungrouped = forums.filter(
    (f) => !f.category_id || !categories.some((c) => c.id === f.category_id),
  )
  return (
    <>
      <PageHeader title="Forums">
        <CategoryCreate onCreated={refresh} />
        <ForumCreateDialog disabled={busy} categories={categories} onCreated={refresh} />
      </PageHeader>
      <div className="flex gap-2 border-b border-zinc-800 px-4 py-3">
        <select
          aria-label="Archive filter"
          className="border border-zinc-800 bg-zinc-950 px-2 text-xs"
          value={archived}
          onChange={(e) => {
            setBusy(true)
            setArchived(e.target.value)
          }}
        >
          <option value="">All forums</option>
          <option value="false">Active forums</option>
          <option value="true">Archived forums</option>
        </select>
        <Button variant="outline" size="sm" onClick={refresh}>
          Refresh
        </Button>
      </div>
      <section aria-label="Forum directory" className="overflow-x-auto">
        {error && (
          <div role="alert" className="flex gap-3 p-4 text-xs text-red-300">
            {error}
            <Button variant="outline" onClick={refresh}>
              Retry
            </Button>
          </div>
        )}
        {busy && (
          <p role="status" className="p-4 text-xs text-zinc-400">
            Loading forums…
          </p>
        )}
        <div className="min-w-[36rem]">
          <div
            className={`${columns} border-b border-zinc-800 py-2 pl-7 pr-4 text-[10px] uppercase tracking-wider text-zinc-500`}
          >
            <span>Forum</span>
            <span className="text-center">Posts</span>
            <span className="pl-3">Last post</span>
            <span />
          </div>
          {categories
            .slice()
            .sort(forumOrder)
            .map((c) => (
              <section key={c.id} aria-label={c.title}>
                <div className="flex items-center justify-between gap-3 border-b border-zinc-800 bg-zinc-900/30 px-4 py-3">
                  <div>
                    <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-300">
                      {c.title}
                      {c.archived && ' · Archived'}
                    </h2>
                    {c.description && (
                      <p className="mt-1 text-xs text-zinc-500">{c.description}</p>
                    )}
                  </div>
                  <ForumManagement
                    kind="category"
                    item={c}
                    readOnly={c.archived}
                    onChanged={refresh}
                  />
                </div>
                {forums
                  .filter((f) => f.category_id === c.id)
                  .sort(forumOrder)
                  .map((f) => boardRow(f, c.archived))}
                {!forums.some((f) => f.category_id === c.id) && (
                  <p className="px-7 py-3 text-xs text-zinc-500">
                    No forums match this view.
                  </p>
                )}
              </section>
            ))}
          {ungrouped.length > 0 && (
            <section aria-label="Uncategorized forums">
              <h2 className="border-b border-zinc-800 bg-zinc-900/30 px-4 py-3 text-xs font-semibold uppercase tracking-wider text-zinc-300">
                Forums
              </h2>
              {ungrouped.sort(forumOrder).map((f) => boardRow(f))}
            </section>
          )}
        </div>
        {!busy && !error && !forums.length && !categories.length && (
          <EmptyState
            variant="empty"
            title="No forums available"
            description="Create a category or forum, or adjust your filters."
          />
        )}
      </section>
    </>
  )
}

function CategoryCreate({ onCreated }: { onCreated: () => void }) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [id, setID] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [sort, setSort] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return (
    <>
      <Button
        size="sm"
        variant="outline"
        onClick={() => {
          setOpen(true)
          setTitle('')
          setID('')
          setDescription('')
          setSort(0)
          setError('')
        }}
      >
        New category
      </Button>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!busy) setOpen(v)
        }}
      >
        <DialogContent className="rounded-none" widthClassName="max-w-md">
          <DialogHeader>
            <DialogTitle>New category</DialogTitle>
            <DialogDescription>Group related forums.</DialogDescription>
          </DialogHeader>
          <form
            className="space-y-3"
            onSubmit={async (e) => {
              e.preventDefault()
              if (busy) return
              setBusy(true)
              setError('')
              try {
                if (!Number.isSafeInteger(sort))
                  throw new Error('Sort order must be a safe integer.')
                await api.forumCreateCategory({
                  id,
                  title: title.trim(),
                  description,
                  sort_order: sort,
                })
                setOpen(false)
                onCreated()
              } catch (err) {
                setError(err instanceof Error ? err.message : 'Could not create category.')
              } finally {
                setBusy(false)
              }
            }}
          >
            <fieldset disabled={busy} className="space-y-3">
              <label className="block text-xs">
                Category name
                <Input
                  aria-label="Category name"
                  className={forumFieldClass}
                  required
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                />
              </label>
              <label className="block text-xs">
                Category ID
                <Input
                  aria-label="Category ID"
                  className={forumFieldClass}
                  required
                  pattern="[a-zA-Z0-9][a-zA-Z0-9_\-]{0,127}"
                  value={id}
                  onChange={(e) => setID(e.target.value)}
                />
              </label>
              <label className="block text-xs">
                Description
                <Textarea
                  aria-label="Category description"
                  className={forumFieldClass}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </label>
              <label className="block text-xs">
                Sort order
                <Input
                  aria-label="Category sort order"
                  type="number"
                  step="1"
                  required
                  min={Number.MIN_SAFE_INTEGER}
                  max={Number.MAX_SAFE_INTEGER}
                  className={forumFieldClass}
                  value={sort}
                  onChange={(e) => setSort(e.target.valueAsNumber)}
                />
              </label>
            </fieldset>
            {error && (
              <p role="alert" className="text-xs text-red-300">
                {error}
              </p>
            )}
            <Button disabled={busy || !title.trim()} type="submit">
              {busy ? 'Creating…' : 'Create category'}
            </Button>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
