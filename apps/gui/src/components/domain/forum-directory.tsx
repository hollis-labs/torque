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
import type { Forum, ForumCategory } from '@/lib/forums'
import { ForumCreateDialog } from './forum-create-dialog'
import { ForumManagement, forumFieldClass } from './forum-management'

export function ForumDirectory() {
  const api = useApi()
  const [forums, setForums] = useState<Forum[]>([])
  const [categories, setCategories] = useState<ForumCategory[]>([])
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [namespaceInput, setNamespaceInput] = useState('')
  const [namespace, setNamespace] = useState('')
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
        { namespace: namespace || undefined, archived: archived || undefined },
        c.signal,
      ),
      allForumPages(
        (opts, signal) => api.forumCategories(opts, signal),
        { namespace: namespace || undefined },
        c.signal,
      ),
    ])
      .then(([boards, cats]) => {
        if (!c.signal.aborted) {
          setForums(boards)
          setCategories(cats)
          setError('')
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(e instanceof Error ? e.message : 'Could not load forums.')
      })
      .finally(() => {
        if (!c.signal.aborted) setBusy(false)
      })
    return () => c.abort()
  }, [api, namespace, archived, attempt])
  const namespaces = [...new Set([...forums, ...categories].map((f) => f.namespace))].sort()
  function boardRow(f: Forum, inheritedArchive = false) {
    return (
      <div
        key={f.id}
        className="flex items-center justify-between gap-3 border-b border-zinc-800/60 px-4 py-3"
      >
        <Link
          className="min-w-0 flex-1 hover:text-primary"
          to={`/forums/${encodeURIComponent(f.id)}`}
        >
          <h3 className="text-[13px] font-medium">
            {f.title}{' '}
            {(f.archived || inheritedArchive) && (
              <span className="ml-2 text-[10px] uppercase text-zinc-500">
                {f.archived ? 'Archived' : 'Category archived'}
              </span>
            )}
          </h3>
          {f.description && <p className="mt-1 text-xs text-zinc-400">{f.description}</p>}
        </Link>
        <ForumManagement
          kind="forum"
          item={f}
          readOnly={f.archived || inheritedArchive}
          onChanged={refresh}
        />
      </div>
    )
  }
  return (
    <>
      <PageHeader title="Forums">
        <CategoryCreate onCreated={refresh} namespace={namespace || namespaces[0]} />
        <ForumCreateDialog
          disabled={busy}
          namespaces={namespaces}
          categories={categories}
          onCreated={(f) => {
            setForums((old) => [...old.filter((v) => v.id !== f.id), f].sort(forumOrder))
            // Restart list cursors after insertion, retaining the successful create while loading.
            refresh()
          }}
        />
      </PageHeader>
      <form
        className="flex flex-wrap gap-2 border-b border-zinc-800 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault()
          setNamespace(namespaceInput.trim())
          refresh()
        }}
      >
        <Input
          className="max-w-sm rounded-none"
          aria-label="Namespace filter"
          placeholder="All namespaces"
          value={namespaceInput}
          onChange={(e) => setNamespaceInput(e.target.value)}
        />
        <select
          aria-label="Archive filter"
          className="border border-zinc-800 bg-zinc-950 px-2 text-xs"
          value={archived}
          onChange={(e) => {
            setBusy(true)
            setArchived(e.target.value)
          }}
        >
          <option value="">All boards</option>
          <option value="false">Active boards</option>
          <option value="true">Archived boards</option>
        </select>
        <Button type="submit" variant="outline" size="sm">
          Apply
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setNamespaceInput('')
            setNamespace('')
            setArchived('')
            refresh()
          }}
        >
          Reset
        </Button>
      </form>
      <section aria-label="Forum directory">
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
        {namespaces.map((ns) => (
          <section key={ns} aria-label={ns}>
            <div className="border-b border-zinc-800 bg-zinc-900/30 px-4 py-3 font-mono text-[11px] text-zinc-500">
              {ns}
            </div>
            {forums
              .filter((f) => f.namespace === ns && !f.category_id)
              .sort(forumOrder)
              .map((f) => boardRow(f))}
            {categories
              .filter((c) => c.namespace === ns)
              .sort(forumOrder)
              .map((c) => (
                <section key={c.id} aria-label={c.title}>
                  <div className="flex items-center justify-between gap-3 border-b border-zinc-800 bg-zinc-900/20 px-4 py-3">
                    <div>
                      <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-400">
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
                    .filter((f) => f.namespace === ns && f.category_id === c.id)
                    .sort(forumOrder)
                    .map((f) => boardRow(f, c.archived))}
                  {!forums.some((f) => f.category_id === c.id) && (
                    <p className="px-4 py-3 text-xs text-zinc-500">No boards match this view.</p>
                  )}
                </section>
              ))}
            {forums
              .filter(
                (f) =>
                  f.namespace === ns &&
                  f.category_id &&
                  !categories.some((c) => c.id === f.category_id),
              )
              .sort(forumOrder)
              .map((f) => boardRow(f))}
          </section>
        ))}
        {!busy && !error && !namespaces.length && (
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

function CategoryCreate({ onCreated, namespace }: { onCreated: () => void; namespace?: string }) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [ns, setNS] = useState('')
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
          setNS(namespace || 'app/torque/forum/general')
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
            <DialogDescription>Group boards within a namespace.</DialogDescription>
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
                  namespace: ns,
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
                Namespace
                <Input
                  aria-label="Category namespace"
                  className={forumFieldClass}
                  required
                  value={ns}
                  onChange={(e) => setNS(e.target.value)}
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
