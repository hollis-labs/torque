import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Check, ExternalLink, Pencil, Plus, Trash2 } from 'lucide-react'
import {
  Button,
  CopyableId,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Skeleton,
  Textarea,
} from '@hollis-labs/sysop-ui'
import { PlannerFields } from '@/components/planner/planner-fields'
import { PlannerStatus } from '@/components/planner/planner-status'
import { useApi } from '@/hooks/use-api'
import { usePlannerSchema } from '@/hooks/use-planner-schema'
import { ApiError } from '@/lib/api'
import {
  PLANNER_DBS,
  buildPatch,
  conflictCurrent,
  dbOfId,
  draftFrom,
  fieldSpecs,
  itemFacts,
  linkTarget,
  plannerMeta,
  validateLinkDraft,
  validationErrors,
  type Draft,
  type FieldError,
} from '@/lib/planner'
import { notifyError, notifySuccess } from '@/lib/toast'
import type { PlannerDb, PlannerItem, PlannerLink } from '@/lib/types'

interface PlannerDetailDialogProps {
  db: PlannerDb
  id: string
  /** Bumped by the page when the server says something changed. */
  refreshKey?: unknown
  onClose: () => void
  /** The item changed: the page reloads its list. */
  onChanged: () => void
  /** Open another planner item (a reference chip, a promoted item). */
  onOpen: (db: PlannerDb, id: string) => void
}

const REFERENCE_FIELDS = (name: string) => name === 'related_ids' || name === 'depends_on' || name.endsWith('_ids')

function when(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function PlannerDetailDialog({ db, id, refreshKey, onClose, onChanged, onOpen }: PlannerDetailDialogProps) {
  const api = useApi()
  const schema = usePlannerSchema(db)
  const meta = plannerMeta(db)
  const [item, setItem] = useState<PlannerItem | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Draft>({})
  const [fieldErrors, setFieldErrors] = useState<FieldError[]>([])
  const [busy, setBusy] = useState(false)

  const specs = useMemo(() => (schema ? fieldSpecs(schema) : []), [schema])

  const load = useCallback(async () => {
    try {
      setItem(await api.plannerGet(db, id))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load')
    }
  }, [api, db, id])

  useEffect(() => {
    setItem(null)
    setEditing(false)
    void load()
  }, [load, refreshKey])

  /** Run a write; put its result on screen and tell the page. */
  async function write<T extends PlannerItem>(fn: () => Promise<T>, failure: string): Promise<T | null> {
    setBusy(true)
    try {
      const next = await fn()
      setItem(next)
      onChanged()
      return next
    } catch (err) {
      notifyError(err, failure)
      return null
    } finally {
      setBusy(false)
    }
  }

  function startEdit() {
    if (!item) return
    setDraft(draftFrom(specs, item))
    setFieldErrors([])
    setEditing(true)
  }

  async function saveEdit() {
    if (!item) return
    const patch = buildPatch(specs, item, draft)
    if (Object.keys(patch).length === 0) {
      setEditing(false)
      return
    }
    setBusy(true)
    setFieldErrors([])
    try {
      const next = await api.plannerUpdate(db, id, patch, item.rev)
      setItem(next)
      setEditing(false)
      onChanged()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid') {
        setFieldErrors(validationErrors(err.details))
        notifyError(err, 'Some fields are not valid')
      } else if (err instanceof ApiError && err.code === 'conflict') {
        const current = conflictCurrent(err.details)
        if (current) setItem(current)
        setEditing(false)
        notifyError(new Error('Someone else changed this item. Showing the latest version; make your edit again.'), 'Edit conflict')
      } else {
        notifyError(err, 'Failed to save')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[88vh] overflow-auto sm:max-w-3xl" aria-describedby={undefined}>
        {error ? (
          <>
            <DialogHeader>
              <DialogTitle>{id}</DialogTitle>
              <DialogDescription>{error}</DialogDescription>
            </DialogHeader>
          </>
        ) : !item ? (
          <>
            <DialogHeader>
              <DialogTitle>{id}</DialogTitle>
            </DialogHeader>
            <Skeleton className="h-24 w-full" />
          </>
        ) : (
          <div className="flex flex-col gap-5">
            <DialogHeader>
              <div className="flex flex-wrap items-center gap-2 pr-8">
                <CopyableId id={item.id} />
                <PlannerStatus status={item.status} />
                {itemFacts(db, item).map((f) => (
                  <span key={f} className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                    {f}
                  </span>
                ))}
              </div>
              <DialogTitle className="text-lg">{item.title}</DialogTitle>
              {item.tldr && <DialogDescription className="whitespace-pre-wrap">{item.tldr}</DialogDescription>}
            </DialogHeader>

            <div className="flex flex-wrap items-center gap-2">
              {!editing && (
                <Button size="sm" variant="outline" className="gap-1" onClick={startEdit} disabled={!schema || busy}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
              )}
              <span className="ml-auto text-xs text-muted-foreground">
                rev {item.rev}
                {item.updated ? ` · updated ${item.updated}` : ''}
                {item.author ? ` · ${item.author}` : ''}
              </span>
            </div>

            {db === 'decisions' && <DecisionPanel item={item} busy={busy} write={write} />}
            {db === 'inbox' && <InboxPanel item={item} busy={busy} write={write} onOpen={onOpen} onChanged={onChanged} />}

            {editing ? (
              <div className="flex flex-col gap-3 rounded-lg border border-border p-4">
                <PlannerFields specs={specs} draft={draft} errors={fieldErrors} idPrefix="pe" onChange={(name, value) => setDraft((d) => ({ ...d, [name]: value }))} />
                <div className="flex justify-end gap-2">
                  <Button variant="outline" size="sm" onClick={() => setEditing(false)} disabled={busy}>
                    Cancel
                  </Button>
                  <Button size="sm" onClick={saveEdit} disabled={busy}>
                    {busy ? 'Saving…' : 'Save'}
                  </Button>
                </div>
              </div>
            ) : (
              <FieldList item={item} specs={specs.map((s) => s.name)} schemaLabels={Object.fromEntries(specs.map((s) => [s.name, s.label]))} onOpen={onOpen} />
            )}

            <LinksPanel
              item={item}
              kinds={schema?.link_kinds ?? []}
              busy={busy}
              onAdd={(link) => write(() => api.plannerLinkAdd(db, id, link), 'Failed to add link')}
              onRemove={(link) => write(() => api.plannerLinkRemove(db, id, link.kind, link.ref), 'Failed to remove link')}
            />

            <CommentsPanel item={item} busy={busy} onAdd={(text) => write(() => api.plannerComment(db, id, text), 'Failed to add comment')} />
          </div>
        )}
        <span className="sr-only">{meta.label}</span>
      </DialogContent>
    </Dialog>
  )
}

// ---------- fields (read-only view) ----------

const SKIP_IN_LIST = new Set(['title', 'tldr', 'status', 'kind'])

function FieldList({ item, specs, schemaLabels, onOpen }: { item: PlannerItem; specs: string[]; schemaLabels: Record<string, string>; onOpen: (db: PlannerDb, id: string) => void }) {
  const names = specs.filter((n) => !SKIP_IN_LIST.has(n))
  const rows = names.filter((n) => {
    const v = item[n]
    return v !== undefined && v !== null && v !== '' && v !== false && !(Array.isArray(v) && v.length === 0)
  })
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No other fields set.</p>
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-[10rem_1fr]">
      {rows.map((n) => (
        <div key={n} className="contents">
          <dt className="text-xs uppercase tracking-wide text-muted-foreground">{schemaLabels[n] ?? n}</dt>
          <dd className="min-w-0 text-sm">
            <FieldValue name={n} value={item[n]} onOpen={onOpen} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

function FieldValue({ name, value, onOpen }: { name: string; value: unknown; onOpen: (db: PlannerDb, id: string) => void }) {
  if (Array.isArray(value) && REFERENCE_FIELDS(name)) {
    return (
      <div className="flex flex-wrap gap-1.5">
        {value.map(String).map((ref) => {
          const target = dbOfId(ref)
          return target ? (
            <button key={ref} type="button" className="rounded border border-border px-1.5 py-0.5 font-mono text-xs hover:bg-muted" onClick={() => onOpen(target, ref)}>
              {ref}
            </button>
          ) : (
            <span key={ref} className="rounded border border-border px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
              {ref}
            </span>
          )
        })}
      </div>
    )
  }
  if (Array.isArray(value)) {
    return (
      <ul className="list-disc pl-4">
        {value.map((v, i) => (
          <li key={i}>{String(v)}</li>
        ))}
      </ul>
    )
  }
  if (typeof value === 'boolean') return <span>{value ? 'Yes' : 'No'}</span>
  return <span className="whitespace-pre-wrap break-words">{String(value)}</span>
}

// ---------- links ----------

function LinksPanel({ item, kinds, busy, onAdd, onRemove }: { item: PlannerItem; kinds: string[]; busy: boolean; onAdd: (l: PlannerLink) => Promise<unknown>; onRemove: (l: PlannerLink) => Promise<unknown> }) {
  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState('torque')
  const [ref, setRef] = useState('')
  const [label, setLabel] = useState('')
  const problem = ref ? validateLinkDraft(kind, ref) : null
  const links = item.links ?? []

  async function add() {
    const done = await onAdd({ kind, ref: ref.trim(), ...(label.trim() ? { label: label.trim() } : {}) })
    if (done) {
      setRef('')
      setLabel('')
      setAdding(false)
    }
  }

  return (
    <section aria-label="Links" className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Links</h3>
        {!adding && (
          <Button size="sm" variant="ghost" className="gap-1" onClick={() => setAdding(true)} disabled={busy || kinds.length === 0}>
            <Plus className="h-3.5 w-3.5" /> Add link
          </Button>
        )}
      </div>
      {links.length === 0 && !adding && <p className="text-sm text-muted-foreground">Nothing linked. Point this at Torque tasks, PRs, notes or files.</p>}
      <ul className="flex flex-col gap-1">
        {links.map((l) => {
          const target = linkTarget(l)
          const text = l.label ? `${l.label} (${l.ref})` : l.ref
          return (
            <li key={`${l.kind}:${l.ref}`} className="flex items-center gap-2 text-sm">
              <span className="w-20 shrink-0 text-xs uppercase tracking-wide text-muted-foreground">{l.kind}</span>
              {target.to ? (
                <Link to={target.to} className="min-w-0 truncate font-mono text-primary hover:underline">
                  {text}
                </Link>
              ) : target.href ? (
                <a href={target.href} target="_blank" rel="noreferrer noopener" className="inline-flex min-w-0 items-center gap-1 truncate text-primary hover:underline">
                  {text} <ExternalLink className="h-3 w-3 shrink-0" />
                </a>
              ) : (
                <span className="min-w-0 truncate font-mono">{text}</span>
              )}
              <Button size="sm" variant="ghost" className="ml-auto h-6 w-6 p-0" aria-label={`Remove ${l.kind} link ${l.ref}`} onClick={() => onRemove(l)} disabled={busy}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </li>
          )
        })}
      </ul>
      {adding && (
        <div className="flex flex-col gap-2 rounded-lg border border-border p-3">
          <div className="grid gap-2 sm:grid-cols-[9rem_1fr_1fr]">
            <Select value={kind} onValueChange={(v) => v && setKind(v)}>
              <SelectTrigger className="h-8 text-sm" aria-label="Link kind">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {kinds.map((k) => (
                  <SelectItem key={k} value={k}>
                    {k}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input value={ref} onChange={(e) => setRef(e.target.value)} placeholder="Task id, URL, path…" aria-label="Link target" />
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Label (optional)" aria-label="Link label" />
          </div>
          {problem && <p className="text-xs text-destructive">{problem}</p>}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={() => setAdding(false)}>
              Cancel
            </Button>
            <Button size="sm" onClick={add} disabled={busy || !ref.trim() || !!problem}>
              Add link
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}

// ---------- comments ----------

function CommentsPanel({ item, busy, onAdd }: { item: PlannerItem; busy: boolean; onAdd: (text: string) => Promise<unknown> }) {
  const [text, setText] = useState('')
  const comments = item.comments ?? []

  async function add() {
    const t = text.trim()
    if (!t) return
    if (await onAdd(t)) setText('')
  }

  return (
    <section aria-label="Comments" className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">Comments{comments.length ? ` (${comments.length})` : ''}</h3>
      <ul className="flex flex-col gap-3">
        {comments.map((c) => (
          <li key={c.id} className={`rounded-lg border p-3 text-sm ${c.kind === 'decision' ? 'border-primary/40 bg-primary/5' : 'border-border'}`}>
            <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
              <span className="font-medium text-foreground">{c.author || 'unknown'}</span>
              <span>{when(c.created)}</span>
              {c.kind === 'decision' && <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">decision</span>}
            </div>
            <p className="whitespace-pre-wrap break-words">{c.text}</p>
          </li>
        ))}
      </ul>
      <Label htmlFor="planner-new-comment" className="sr-only">
        New comment
      </Label>
      <Textarea
        id="planner-new-comment"
        value={text}
        rows={2}
        placeholder="Add a comment…"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault()
            void add()
          }
        }}
      />
      <div className="flex justify-end">
        <Button size="sm" onClick={add} disabled={busy || !text.trim()}>
          Comment
        </Button>
      </div>
    </section>
  )
}

// ---------- decisions ----------

type Write = <T extends PlannerItem>(fn: () => Promise<T>, failure: string) => Promise<T | null>

function DecisionPanel({ item, busy, write }: { item: PlannerItem; busy: boolean; write: Write }) {
  const api = useApi()
  const [comment, setComment] = useState('')
  const options = item.options ?? []
  const open = item.status === 'needs-decision' || item.status === 'deferred'
  const chosen = typeof item.selected_option === 'string' ? item.selected_option : ''

  return (
    <section aria-label="Decision" className="flex flex-col gap-3 rounded-lg border border-border p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Decision</h3>
        {item.status !== 'needs-decision' && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => write(() => api.plannerReopen(item.id), 'Failed to reopen')}>
            Reopen
          </Button>
        )}
      </div>
      {item.status === 'deferred' && typeof item.defer_note === 'string' && item.defer_note && (
        <p className="text-sm text-muted-foreground">
          <span className="font-medium text-foreground">Deferred: </span>
          {item.defer_note}
        </p>
      )}
      {typeof item.decision === 'string' && item.decision && <p className="whitespace-pre-wrap text-sm">{item.decision}</p>}
      {options.length > 0 && (
        <ul className="flex flex-col gap-2">
          {options.map((o) => (
            <li key={o.id} className={`rounded-lg border p-3 text-sm ${o.id === chosen ? 'border-primary bg-primary/5' : 'border-border'}`}>
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs text-muted-foreground">{o.id}</span>
                <span className="font-medium">{o.label}</span>
                {o.recommended && <span className="rounded bg-muted px-1.5 py-0.5 text-[11px]">recommended</span>}
                {o.id === chosen && (
                  <span className="inline-flex items-center gap-1 text-xs text-primary">
                    <Check className="h-3 w-3" /> chosen
                  </span>
                )}
                {open && (
                  <Button
                    size="sm"
                    className="ml-auto"
                    disabled={busy}
                    aria-label={`Choose option ${o.id}: ${o.label}`}
                    onClick={() => write(() => api.plannerDecide(item.id, o.id, comment.trim() || undefined), 'Failed to record the decision')}
                  >
                    Choose
                  </Button>
                )}
              </div>
              {o.summary && <p className="mt-1 text-muted-foreground">{o.summary}</p>}
              {(o.pros || o.cons) && (
                <div className="mt-2 grid gap-2 sm:grid-cols-2">
                  {o.pros && <p><span className="font-medium">Pros: </span>{o.pros}</p>}
                  {o.cons && <p><span className="font-medium">Cons: </span>{o.cons}</p>}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {options.length === 0 && open && <p className="text-sm text-muted-foreground">This decision has no options to choose from yet.</p>}
      {open && (
        <div className="flex flex-col gap-2">
          <Textarea value={comment} rows={2} placeholder="Why (recorded with the decision)…" aria-label="Decision comment" onChange={(e) => setComment(e.target.value)} />
          {item.status === 'needs-decision' && (
            <div>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => write(() => api.plannerDefer(item.id, comment.trim() || undefined), 'Failed to defer')}>
                Defer
              </Button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

// ---------- inbox ----------

function InboxPanel({ item, busy, write, onOpen, onChanged }: { item: PlannerItem; busy: boolean; write: Write; onOpen: (db: PlannerDb, id: string) => void; onChanged: () => void }) {
  const api = useApi()
  const [to, setTo] = useState<PlannerDb>('ideas')
  const [note, setNote] = useState('')
  const [promoting, setPromoting] = useState(false)
  const triageable = item.status === 'new' || item.status === 'triaged'
  const promoted = item.promoted_to as { db?: string; id?: string } | undefined

  if (!triageable) {
    return promoted?.id && promoted.db ? (
      <p className="text-sm">
        Promoted to{' '}
        <button type="button" className="font-mono text-primary hover:underline" onClick={() => onOpen(promoted.db as PlannerDb, promoted.id as string)}>
          {promoted.id}
        </button>
      </p>
    ) : null
  }

  async function promote() {
    setPromoting(true)
    try {
      const res = await api.plannerInboxPromote(item.id, to)
      notifySuccess(`Promoted to ${res.item.id}`)
      onChanged()
      onOpen(to, res.item.id)
    } catch (err) {
      notifyError(err, 'Failed to promote')
    } finally {
      setPromoting(false)
    }
  }

  return (
    <section aria-label="Triage" className="flex flex-col gap-3 rounded-lg border border-border p-4">
      <h3 className="text-sm font-semibold">Triage</h3>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm">Promote to</span>
        <Select value={to} onValueChange={(v) => v && setTo(v as PlannerDb)}>
          <SelectTrigger className="h-8 w-40 text-sm" aria-label="Promote to">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {PLANNER_DBS.filter((d) => d.key !== 'inbox').map((d) => (
              <SelectItem key={d.key} value={d.key}>
                {d.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button size="sm" onClick={promote} disabled={busy || promoting}>
          Promote
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input className="max-w-sm" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Why dismiss (optional)" aria-label="Dismiss note" />
        <Button size="sm" variant="outline" disabled={busy || promoting} onClick={() => write(() => api.plannerInboxDismiss(item.id, note.trim() || undefined), 'Failed to dismiss')}>
          Dismiss
        </Button>
      </div>
    </section>
  )
}
