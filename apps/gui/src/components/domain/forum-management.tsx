import { useEffect, useRef, useState } from 'react'
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Textarea,
} from '@hollis-labs/sysop-ui'
import { useApi } from '@/hooks/use-api'
import { allForumPages, safeAttachmentURL } from '@/lib/forums'
import type {
  Forum,
  ForumAttachment,
  ForumCategory,
  ForumMutation,
  ForumPost,
  ForumResource,
  ForumTopic,
} from '@/lib/forums'

export const forumFieldClass =
  'w-full rounded-none border border-zinc-800 bg-zinc-950 p-2 text-xs text-zinc-200 [color-scheme:dark]'
const errorMessage = (e: unknown) => (e instanceof Error ? e.message : 'The forum request failed.')

type Resource = Forum | ForumCategory | ForumTopic | ForumPost
type Action = 'edit' | 'archive' | 'unarchive' | 'delete' | 'move' | 'clone' | 'fork'

export function AttachmentFields({
  value,
  onChange,
  disabled = false,
}: {
  value: ForumAttachment[]
  onChange: (value: ForumAttachment[]) => void
  disabled?: boolean
}) {
  return (
    <fieldset disabled={disabled} className="space-y-2">
      <legend className="mb-2 text-xs text-zinc-400">Attachments (links)</legend>
      {value.map((attachment, i) => (
        <div key={i} className="flex flex-wrap gap-2">
          <Input
            aria-label={`Attachment ${i + 1} name`}
            placeholder="Name"
            required
            value={attachment.name}
            className={forumFieldClass}
            onChange={(e) =>
              onChange(value.map((a, n) => (n === i ? { ...a, name: e.target.value } : a)))
            }
          />
          <Input
            aria-label={`Attachment ${i + 1} URL`}
            placeholder="Absolute URL"
            type="url"
            required
            value={attachment.uri}
            className={forumFieldClass}
            onChange={(e) =>
              onChange(value.map((a, n) => (n === i ? { ...a, uri: e.target.value } : a)))
            }
          />
          <Input
            aria-label={`Attachment ${i + 1} media type`}
            placeholder="Media type (optional)"
            value={attachment.media_type ?? ''}
            className={forumFieldClass}
            onChange={(e) =>
              onChange(value.map((a, n) => (n === i ? { ...a, media_type: e.target.value } : a)))
            }
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onChange(value.filter((_, n) => n !== i))}
          >
            Remove attachment {i + 1}
          </Button>
        </div>
      ))}
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={value.length >= 20}
        onClick={() => onChange([...value, { name: '', uri: '' }])}
      >
        Add attachment
      </Button>
    </fieldset>
  )
}

export function AttachmentLinks({ attachments }: { attachments?: ForumAttachment[] }) {
  if (!attachments?.length) return null
  return (
    <ul className="mt-3 space-y-1 text-xs" aria-label="Attachments">
      {attachments.map((a, i) => {
        const href = safeAttachmentURL(a.uri)
        return (
          <li key={i}>
            {href ? (
              <a
                className="text-primary underline"
                href={href}
                target="_blank"
                rel="noopener noreferrer"
              >
                {a.name}
              </a>
            ) : (
              <span className="text-zinc-400">
                {a.name} · {a.uri}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

export function ForumManagement({
  kind,
  item,
  namespace,
  readOnly,
  onChanged,
}: {
  kind: ForumResource
  item: Resource
  namespace?: string
  readOnly?: boolean
  onChanged: (result: ForumMutation, action: Action) => void
}) {
  const [open, setOpen] = useState(false)
  const [snapshot, setSnapshot] = useState(item)
  const [busy, setBusy] = useState(false)
  const title = 'title' in item ? item.title : `Post #${item.seq}`
  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        aria-label={`Manage ${title}`}
        onClick={() => {
          setSnapshot(item)
          setOpen(true)
        }}
      >
        Manage
      </Button>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!busy) setOpen(v)
        }}
      >
        {open && (
          <DialogContent className="rounded-none" widthClassName="max-w-xl" showCloseButton={!busy}>
            <DialogHeader>
              <DialogTitle>Manage {title}</DialogTitle>
              <DialogDescription>
                Edit or organize this{' '}
                {kind === 'forum' ? 'board' : kind === 'topic' ? 'thread' : kind}.
              </DialogDescription>
            </DialogHeader>
            <ManagementForm
              kind={kind}
              item={snapshot}
              onBusy={setBusy}
              namespace={namespace}
              readOnly={readOnly}
              onChanged={(out, action) => {
                setOpen(false)
                onChanged(out, action)
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}

function ManagementForm({
  kind,
  item,
  namespace,
  readOnly,
  onChanged,
  onBusy,
}: {
  kind: ForumResource
  item: Resource
  namespace?: string
  readOnly?: boolean
  onChanged: (result: ForumMutation, action: Action) => void
  onBusy: (busy: boolean) => void
}) {
  const api = useApi()
  const [action, setAction] = useState<Action>(
    readOnly ? ('archived' in item && item.archived ? 'unarchive' : 'clone') : 'edit',
  )
  const [title, setTitle] = useState('title' in item ? item.title : '')
  const [description, setDescription] = useState(
    'description' in item ? (item.description ?? '') : '',
  )
  const [sort, setSort] = useState('sort_order' in item ? (item.sort_order ?? 0) : 0)
  const [status, setStatus] = useState('status' in item ? item.status : 'open')
  const [body, setBody] = useState('')
  const [subject, setSubject] = useState('')
  const [attachments, setAttachments] = useState<ForumAttachment[]>([])
  const [original, setOriginal] = useState<ForumPost | null>(null)
  const [boardOriginal, setBoardOriginal] = useState<Forum | null>(null)
  const [config, setConfig] = useState('{}')
  const [metadata, setMetadata] = useState('{}')
  const [categories, setCategories] = useState<ForumCategory[]>([])
  const [forums, setForums] = useState<Forum[]>([])
  const [topics, setTopics] = useState<ForumTopic[]>([])
  const [parents, setParents] = useState<ForumPost[]>([])
  const [destination, setDestination] = useState(
    'category_id' in item ? (item.category_id ?? '') : '',
  )
  const [destinationTopic, setDestinationTopic] = useState('')
  const [parent, setParent] = useState('')
  const [newID, setNewID] = useState('')
  const [cascade, setCascade] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const pending = useRef(false)
  const archived = 'archived' in item && item.archived
  const transfer = ['move', 'clone', 'fork'].includes(action)
  // Fetch complete destination collections. Namespace checks are also enforced by Tether.
  useEffect(() => {
    const c = new AbortController()
    async function load() {
      try {
        if (kind === 'post' && action === 'edit') {
          const { post } = await api.forumPost(item.id, c.signal)
          if (post.truncated || post.body_md === undefined)
            throw new Error(
              'The full post could not be loaded. Editing a preview would lose content.',
            )
          if (!c.signal.aborted) {
            setOriginal(post)
            setBody(post.body_md)
            setSubject(post.subject ?? '')
            setAttachments(post.attachments ?? [])
          }
        }
        if (kind === 'forum' && action === 'edit') {
          const { forum } = await api.forumGet(item.id, c.signal)
          if (!c.signal.aborted) {
            setBoardOriginal(forum)
            setConfig(JSON.stringify(forum.config_json ?? {}, null, 2))
            setMetadata(JSON.stringify(forum.metadata_json ?? {}, null, 2))
          }
        }
        if (transfer) {
          const ns =
            namespace ??
            ('namespace' in item
              ? item.namespace
              : (await api.forumGet((item as ForumTopic | ForumPost).forum_id, c.signal)).forum
                  .namespace)
          const cats = await allForumPages(
            (opts, signal) => api.forumCategories(opts, signal),
            { namespace: ns },
            c.signal,
          )
          if (!c.signal.aborted) setCategories(cats)
          if (kind !== 'forum') {
            const boards = await allForumPages(
              (opts, signal) => api.forumsList(opts, signal),
              { namespace: ns, archived: false },
              c.signal,
            )
            if (!c.signal.aborted)
              setForums(
                boards.filter((f) => !cats.find((cat) => cat.id === f.category_id)?.archived),
              )
            if (kind === 'post' && action !== 'fork' && destination) {
              const threads = await allForumPages(
                (opts, signal) => api.forumTopics(destination, opts, signal),
                { archived: false },
                c.signal,
              )
              if (!c.signal.aborted) setTopics(threads)
              if (destinationTopic) {
                const posts = await allForumPages(
                  (opts, signal) => api.forumPosts(destinationTopic, opts, signal),
                  {},
                  c.signal,
                )
                if (!c.signal.aborted) setParents(posts)
              }
            }
          }
        }
      } catch (e) {
        if (!c.signal.aborted) setLoadError(errorMessage(e))
      } finally {
        if (!c.signal.aborted) setLoading(false)
      }
    }
    void load()
    return () => c.abort()
  }, [api, kind, item, namespace, action, transfer, attempt, destination, destinationTopic])

  async function submit() {
    if (
      pending.current ||
      loading ||
      loadError ||
      !validDestination ||
      (action === 'delete' && !confirmed)
    )
      return
    pending.current = true
    setBusy(true)
    onBusy(true)
    setError('')
    try {
      let out: ForumMutation
      if (action === 'delete') out = await api.forumDelete(kind, item.id, cascade)
      else if (action === 'edit') {
        const changes: Record<string, unknown> = {}
        if (kind === 'post') {
          if (!original || !body.trim())
            throw new Error('Load the full post and supply a message before saving.')
          if (body !== original.body_md) changes.body_md = body
          if (subject !== (original.subject ?? '')) changes.subject = subject
          if (JSON.stringify(attachments) !== JSON.stringify(original.attachments ?? []))
            changes.attachments = attachments
        } else {
          if (title !== ('title' in item ? item.title : '')) changes.title = title.trim()
          if (kind === 'topic') {
            if (status !== (item as ForumTopic).status) changes.status = status
          } else {
            if (kind === 'forum') {
              if (!boardOriginal) throw new Error('Load board settings before saving.')
              for (const [key, text, originalValue] of [
                ['config_json', config, boardOriginal.config_json],
                ['metadata_json', metadata, boardOriginal.metadata_json],
              ] as const) {
                const value: unknown = JSON.parse(text)
                if (!value || typeof value !== 'object' || Array.isArray(value))
                  throw new Error('Config and metadata must be JSON objects.')
                if (JSON.stringify(value) !== JSON.stringify(originalValue ?? {}))
                  changes[key] = value
              }
            }
            if (description !== ((item as ForumCategory).description ?? ''))
              changes.description = description
            if (!Number.isSafeInteger(sort)) throw new Error('Sort order must be a safe integer.')
            if (sort !== ((item as ForumCategory).sort_order ?? 0)) changes.sort_order = sort
          }
        }
        out = Object.keys(changes).length ? await api.forumUpdate(kind, item.id, changes) : {}
      } else {
        const data: Record<string, unknown> = {}
        if (transfer) {
          if (kind === 'forum') {
            data.destination_category_id = destination
            if (action === 'clone') {
              data.id = newID
              data.title = title.trim()
            }
          } else if (kind === 'topic' || action === 'fork') {
            data.destination_forum_id = destination
            if (action !== 'move') data.title = title.trim()
            if (action === 'fork') data.post_id = item.id
          } else {
            data.destination_topic_id = destinationTopic
            if (parent) data.parent_post_id = parent
          }
        }
        out = await api.forumAction(
          action === 'fork' ? 'topic' : kind,
          action === 'fork' ? (item as ForumPost).topic_id : item.id,
          action,
          data,
        )
      }
      onChanged(out, action)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      pending.current = false
      setBusy(false)
      onBusy(false)
    }
  }
  const validDestination =
    !transfer ||
    (kind === 'forum'
      ? !destination || categories.some((c) => c.id === destination && !c.archived)
      : forums.some((f) => f.id === destination) &&
        (kind !== 'post' || action === 'fork' || topics.some((t) => t.id === destinationTopic)))
  const invalid =
    loading ||
    !!loadError ||
    !validDestination ||
    (action === 'delete' && !confirmed) ||
    (action === 'edit' && (kind === 'post' ? !original || !body.trim() : !title.trim()))
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
      className="space-y-4"
    >
      <fieldset disabled={busy || loading} className="space-y-4">
        <label className="block text-xs">
          Action
          <select
            aria-label="Management action"
            className={forumFieldClass}
            value={action}
            onChange={(e) => {
              setAction(e.target.value as Action)
              setLoading(true)
              setError('')
              setLoadError('')
              setDestination('')
              setDestinationTopic('')
              setParent('')
              setConfirmed(false)
            }}
          >
            <option value="edit" disabled={readOnly}>
              Edit{kind === 'category' || kind === 'forum' ? ' / sort' : ''}
            </option>
            {kind !== 'post' && (
              <option value={archived ? 'unarchive' : 'archive'}>
                {archived ? 'Restore' : 'Archive'}
              </option>
            )}
            {kind !== 'category' && (
              <>
                <option value="move" disabled={readOnly}>
                  Move
                </option>
                <option value="clone">Copy</option>
              </>
            )}
            {kind === 'post' && <option value="fork">Fork into new thread</option>}
            <option value="delete">Delete permanently</option>
          </select>
        </label>
        {readOnly && (
          <p className="text-xs text-amber-300">
            Archived content is read-only. Copies and permanent deletion remain available.
          </p>
        )}
        {((action === 'edit' && kind !== 'post') ||
          (action === 'clone' && kind !== 'post') ||
          action === 'fork') && (
          <label className="block text-xs">
            Title
            <Input
              aria-label="Title"
              className={forumFieldClass}
              required
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
        )}
        {action === 'edit' && (kind === 'category' || kind === 'forum') && (
          <>
            <label className="block text-xs">
              Description
              <Textarea
                aria-label="Description"
                className={forumFieldClass}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </label>
            <label className="block text-xs">
              Sort order
              <Input
                aria-label="Sort order"
                className={forumFieldClass}
                type="number"
                step="1"
                min={Number.MIN_SAFE_INTEGER}
                max={Number.MAX_SAFE_INTEGER}
                required
                value={sort}
                onChange={(e) => setSort(e.target.valueAsNumber)}
              />
            </label>
            <p className="text-xs text-zinc-500">
              Lower numbers appear first. Equal values are ordered by ID.
            </p>
          </>
        )}
        {action === 'edit' && kind === 'forum' && (
          <details>
            <summary className="cursor-pointer text-xs text-zinc-400">Advanced settings</summary>
            <div className="mt-3 space-y-3">
              <label className="block text-xs">
                Config JSON
                <Textarea
                  aria-label="Config JSON"
                  className={forumFieldClass}
                  value={config}
                  onChange={(e) => setConfig(e.target.value)}
                />
              </label>
              <label className="block text-xs">
                Metadata JSON
                <Textarea
                  aria-label="Metadata JSON"
                  className={forumFieldClass}
                  value={metadata}
                  onChange={(e) => setMetadata(e.target.value)}
                />
              </label>
              <p className="text-xs text-zinc-500">
                Each object replaces its previous value. Use an empty object to clear it.
              </p>
            </div>
          </details>
        )}
        {action === 'edit' && kind === 'topic' && (
          <label className="block text-xs">
            Status
            <select
              className={forumFieldClass}
              aria-label="Thread status"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="open">Open</option>
              <option value="resolved">Resolved</option>
            </select>
          </label>
        )}
        {action === 'edit' && kind === 'post' && (
          <>
            <label className="block text-xs">
              Subject
              <Input
                aria-label="Subject"
                className={forumFieldClass}
                maxLength={300}
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
              />
            </label>
            <label className="block text-xs">
              Message
              <Textarea
                aria-label="Message Markdown"
                className={`${forumFieldClass} min-h-40`}
                required
                value={body}
                onChange={(e) => setBody(e.target.value)}
              />
            </label>
            <AttachmentFields value={attachments} onChange={setAttachments} />
          </>
        )}
        {transfer && (
          <>
            <p className="text-xs text-zinc-400">
              Destinations are within the same namespace.{' '}
              {kind === 'post'
                ? 'Includes all replies in this subtree.'
                : 'Includes all descendant content.'}
            </p>
            {kind === 'forum' && action === 'clone' && (
              <label className="block text-xs">
                New board ID
                <Input
                  aria-label="New board ID"
                  className={forumFieldClass}
                  required
                  pattern="[a-zA-Z0-9][a-zA-Z0-9_\-]{0,127}"
                  value={newID}
                  onChange={(e) => setNewID(e.target.value)}
                />
              </label>
            )}
            <label className="block text-xs">
              {kind === 'forum' ? 'Destination category' : 'Destination board'}
              <select
                aria-label="Destination"
                className={forumFieldClass}
                value={destination}
                onChange={(e) => {
                  setLoading(true)
                  setLoadError('')
                  setDestination(e.target.value)
                  setDestinationTopic('')
                  setTopics([])
                  setParent('')
                  setParents([])
                }}
                required={kind !== 'forum'}
              >
                <option value="">{kind === 'forum' ? 'Namespace root' : 'Choose a board'}</option>
                {(kind === 'forum' ? categories.filter((c) => !c.archived) : forums).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.title} ({c.id})
                  </option>
                ))}
              </select>
            </label>
            {kind === 'post' && action !== 'fork' && (
              <>
                <label className="block text-xs">
                  Destination thread
                  <select
                    aria-label="Destination thread"
                    className={forumFieldClass}
                    required
                    value={destinationTopic}
                    onChange={(e) => {
                      setLoading(true)
                      setLoadError('')
                      setDestinationTopic(e.target.value)
                      setParent('')
                      setParents([])
                    }}
                  >
                    <option value="">Choose a thread</option>
                    {topics.map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.title}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="block text-xs">
                  Reply under
                  <select
                    aria-label="Destination parent"
                    className={forumFieldClass}
                    value={parent}
                    onChange={(e) => setParent(e.target.value)}
                  >
                    <option value="">Thread root</option>
                    {parents
                      .filter((p) => action === 'clone' || p.id !== item.id)
                      .map((p) => (
                        <option key={p.id} value={p.id}>
                          #{p.seq} {p.subject || p.snippet?.slice(0, 70)}
                        </option>
                      ))}
                  </select>
                </label>
              </>
            )}
          </>
        )}
        {(action === 'archive' || action === 'unarchive') && (
          <p className="text-xs text-zinc-400">
            {action === 'archive'
              ? 'Content stays readable. New content and edits are blocked for this item and its descendants.'
              : 'Restore this item. Descendants with their own archive flag stay archived.'}
          </p>
        )}
        {action === 'delete' && (
          <div className="space-y-3 text-xs text-red-300">
            <p>Permanent deletion cannot be undone. Archive instead to keep the discussion.</p>
            <label className="flex gap-2">
              <input
                type="checkbox"
                checked={cascade}
                onChange={(e) => setCascade(e.target.checked)}
              />
              Also delete all contained boards, threads, messages and replies
            </label>
            <label className="flex gap-2">
              <input
                type="checkbox"
                required
                checked={confirmed}
                onChange={(e) => setConfirmed(e.target.checked)}
              />
              I confirm permanent deletion of this {kind === 'forum' ? 'board' : kind}
            </label>
          </div>
        )}
      </fieldset>
      {loading && (
        <p role="status" className="text-xs text-zinc-400">
          Loading…
        </p>
      )}
      {loadError && (
        <div role="alert" className="text-xs text-red-300">
          {loadError}{' '}
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setLoading(true)
              setLoadError('')
              setAttempt((n) => n + 1)
            }}
          >
            Retry
          </Button>
        </div>
      )}
      {error && (
        <p role="alert" className="text-xs text-red-300">
          {error}
        </p>
      )}
      <DialogFooter>
        <Button type="submit" disabled={busy || invalid}>
          {busy ? 'Saving…' : action === 'delete' ? 'Delete permanently' : 'Apply changes'}
        </Button>
      </DialogFooter>
    </form>
  )
}
