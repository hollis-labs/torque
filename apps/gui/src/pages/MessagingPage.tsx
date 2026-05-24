import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Bot, Inbox, RefreshCw, Send, User, X } from 'lucide-react'
import {
  PageHeader,
  SummaryCards,
  EmptyState,
  CopyableId,
  Button,
  Input,
  Textarea,
  Skeleton,
} from '@hollis-labs/sysop-ui'
import { ComposeMessageDialog } from '@/components/domain/compose-message-dialog'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { cn } from '@/lib/utils'
import {
  backfillThreadKeys,
  correspondentKey,
  extraPayloadFields,
  formatRelativeTime,
  messageBody,
  messageStatus,
  messageSubject,
  parseUrn,
  shortUrn,
  threadKey,
  type MessageStatus,
} from '@/lib/messaging'
import type { MessageEnvelope } from '@/lib/types'

type ScopeKey = 'user' | 'agent'
type DisplayMessageStatus = MessageStatus | 'canceled'

const SCOPES: { key: ScopeKey; label: string; icon: typeof User; placeholder: string }[] = [
  {
    key: 'user',
    label: 'User',
    icon: User,
    placeholder: 'msg://user/local/operator',
  },
  {
    key: 'agent',
    label: 'Agents',
    icon: Bot,
    placeholder: 'msg://agent/local/orchestrator',
  },
]

const ADDRESS_STORAGE_KEY = 'torque.messaging.addresses'

function loadAddresses(): Record<ScopeKey, string> {
  try {
    const raw = localStorage.getItem(ADDRESS_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<Record<ScopeKey, string>>
      return { user: parsed.user ?? '', agent: parsed.agent ?? '' }
    }
  } catch {
    // ignore malformed storage
  }
  return { user: '', agent: '' }
}

function persistAddresses(addrs: Record<ScopeKey, string>) {
  try {
    localStorage.setItem(ADDRESS_STORAGE_KEY, JSON.stringify(addrs))
  } catch {
    // ignore quota / disabled storage
  }
}

const STATUS_STYLES: Record<DisplayMessageStatus, string> = {
  pending: 'border-amber-500/40 bg-amber-500/10 text-amber-200',
  delivered: 'border-blue-500/40 bg-blue-500/10 text-blue-200',
  consumed: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200',
  canceled: 'border-zinc-600 bg-zinc-800/50 text-zinc-300',
}

function MessageStatusBadge({ status }: { status: DisplayMessageStatus }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] uppercase tracking-wider',
        STATUS_STYLES[status],
      )}
    >
      {status}
    </span>
  )
}

/** Merge envelopes by id (last write wins) and sort newest-first. */
function mergeMessages(
  existing: MessageEnvelope[],
  incoming: MessageEnvelope[],
): MessageEnvelope[] {
  const byId = new Map<string, MessageEnvelope>()
  for (const m of existing) byId.set(m.id, m)
  for (const m of incoming) byId.set(m.id, m)
  return [...byId.values()].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  )
}

/**
 * A rolled-up conversation — every in-memory message exchanged with one
 * correspondent (the other party). Derived client-side; there is no
 * list-conversations endpoint (see the phase-1 design comment).
 */
interface Conversation {
  /** Correspondent URN — the rollup group key. */
  correspondent: string
  /** This conversation's messages, newest-first (parent panel order). */
  messages: MessageEnvelope[]
  /** Newest message — drives the preview, sort order, and reply target. */
  last: MessageEnvelope
  /**
   * Count of still-`pending` messages. Caveat: `getInbox` drains the inbox
   * (pending → delivered), so post-load this reflects only SSE-arrived
   * messages not yet drained — a "new since last drain" hint, not a true
   * unread count.
   */
  unread: number
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 5 }).map((_, i) => (
        <Skeleton key={i} className="h-16 w-full rounded-md" />
      ))}
    </div>
  )
}

export default function MessagingPage() {
  const api = useApi()

  const [scope, setScope] = useState<ScopeKey>('user')
  const [addresses, setAddresses] = useState<Record<ScopeKey, string>>(loadAddresses)
  const [draft, setDraft] = useState('')
  const [panels, setPanels] = useState<Record<ScopeKey, MessageEnvelope[]>>({
    user: [],
    agent: [],
  })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState<Record<ScopeKey, boolean>>({
    user: false,
    agent: false,
  })
  const [selectedCorrespondent, setSelectedCorrespondent] = useState<string | null>(null)
  const [sseStatus, setSseStatus] = useState<'idle' | 'connecting' | 'live' | 'error'>('idle')
  const [composeOpen, setComposeOpen] = useState(false)

  const committedAddress = addresses[scope]
  const messages = panels[scope]
  const scopeMeta = SCOPES.find((s) => s.key === scope)!

  // Keep the address input in sync when switching tabs.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- tab switches should reset the controlled address/detail UI immediately.
    setDraft(addresses[scope])
    setSelectedCorrespondent(null)
    setError(null)
  }, [scope, addresses])

  const mergeIntoScope = useCallback((target: ScopeKey, incoming: MessageEnvelope[]) => {
    setPanels((prev) => ({ ...prev, [target]: mergeMessages(prev[target], incoming) }))
  }, [])

  // Drain the inbox for the committed address (operator-initiated only —
  // see api.getInbox: this marks returned envelopes delivered).
  const loadInbox = useCallback(
    async (addr: string, target: ScopeKey) => {
      if (!addr.trim()) return
      setLoading(true)
      setError(null)
      try {
        const envs = await api.getInbox(addr.trim())
        mergeIntoScope(target, envs)
        setLoaded((prev) => ({ ...prev, [target]: true }))
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load inbox')
      } finally {
        setLoading(false)
      }
    },
    [api, mergeIntoScope],
  )

  function commitAddress() {
    const next = draft.trim()
    if (!next) return
    const updated = { ...addresses, [scope]: next }
    setAddresses(updated)
    persistAddresses(updated)
    loadInbox(next, scope)
  }

  // Live SSE stream for the committed address. Re-subscribes on change.
  useEffect(() => {
    if (!committedAddress.trim()) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- no active address means no live stream.
      setSseStatus('idle')
      return
    }
    const target = scope
    const unsubscribe = api.subscribeMessages(
      committedAddress.trim(),
      (env) => mergeIntoScope(target, [env]),
      (status) => setSseStatus(status),
    )
    return unsubscribe
  }, [api, committedAddress, scope, mergeIntoScope])

  // Roll the flat message list up into one conversation per correspondent.
  // `messages` is newest-first (mergeMessages), so `msgs[0]` is the latest.
  const conversations = useMemo<Conversation[]>(() => {
    if (!committedAddress.trim()) return []
    const groups = new Map<string, MessageEnvelope[]>()
    for (const m of messages) {
      const key = correspondentKey(m, committedAddress)
      const existing = groups.get(key)
      if (existing) existing.push(m)
      else groups.set(key, [m])
    }
    const list: Conversation[] = []
    for (const [correspondent, msgs] of groups) {
      list.push({
        correspondent,
        messages: msgs,
        last: msgs[0],
        unread: msgs.filter((m) => messageStatus(m) === 'pending').length,
      })
    }
    return list.sort(
      (a, b) =>
        new Date(b.last.created_at).getTime() - new Date(a.last.created_at).getTime(),
    )
  }, [messages, committedAddress])

  const selectedConversation = useMemo(
    () => conversations.find((c) => c.correspondent === selectedCorrespondent) ?? null,
    [conversations, selectedCorrespondent],
  )

  const counts = useMemo(() => {
    const pending = messages.filter((m) => messageStatus(m) === 'pending').length
    return { pending, conversations: conversations.length }
  }, [messages, conversations])

  const summaryCards = [
    { label: scopeMeta.label, value: messages.length, subtitle: 'messages' },
    { label: 'Pending', value: counts.pending, accentColor: '#f59e0b' },
    { label: 'Conversations', value: counts.conversations, accentColor: '#60a5fa' },
    {
      label: 'Stream',
      value: sseStatus,
      accentColor:
        sseStatus === 'live' ? '#34d399' : sseStatus === 'error' ? '#f87171' : '#52525b',
    },
  ]

  const addressValid = draft.trim() === '' || parseUrn(draft) !== null

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Messaging">
        <Button size="sm" variant="outline" onClick={() => setComposeOpen(true)}>
          <Send className="h-3.5 w-3.5" />
          Compose
        </Button>
      </PageHeader>

      {/* Scope tabs */}
      <div className="flex items-center gap-1 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2">
        {SCOPES.map((s) => {
          const ScopeIcon = s.icon
          const active = s.key === scope
          return (
            <button
              key={s.key}
              type="button"
              onClick={() => setScope(s.key)}
              className={cn(
                'flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                active
                  ? 'bg-zinc-800 text-zinc-100'
                  : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-300',
              )}
            >
              <ScopeIcon className="h-3.5 w-3.5" />
              {s.label}
              <span className="font-mono text-[10px] text-zinc-500">
                {panels[s.key].length}
              </span>
            </button>
          )
        })}
        <span className="ml-2 text-[11px] text-zinc-600">
          {scope === 'user'
            ? 'Messages addressed to a user URN.'
            : 'Agent-to-agent traffic — anything not addressed to a user.'}
        </span>
      </div>

      {/* Address bar */}
      <div className="flex items-center gap-2 border-b border-zinc-800/80 bg-zinc-950 px-4 py-2">
        <Inbox className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commitAddress()
          }}
          placeholder={scopeMeta.placeholder}
          aria-invalid={!addressValid}
          aria-label="Inbox address URN"
          className="h-8 max-w-md font-mono text-xs"
        />
        <Button
          size="sm"
          onClick={commitAddress}
          disabled={!draft.trim() || !addressValid || loading}
        >
          {loading ? 'Loading…' : 'Load inbox'}
        </Button>
        {loaded[scope] && committedAddress && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => loadInbox(committedAddress, scope)}
            disabled={loading}
            aria-label="Refresh inbox"
          >
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          </Button>
        )}
        <span className="text-[11px] text-zinc-600">
          Draining an inbox marks its messages <span className="text-zinc-500">delivered</span>.
        </span>
      </div>

      <SummaryCards cards={summaryCards} />

      <div className="flex-1 overflow-hidden">
        <div className="grid h-full grid-cols-[minmax(340px,1fr)_minmax(420px,1.4fr)]">
          {/* Conversation list */}
          <div className="flex flex-col overflow-auto border-r border-zinc-800/80">
            {loading && messages.length === 0 ? (
              <ListSkeleton />
            ) : error ? (
              <EmptyState
                variant="error"
                title="Couldn't load inbox"
                description={error}
                action={{
                  label: 'Retry',
                  onClick: () => loadInbox(committedAddress, scope),
                }}
              />
            ) : conversations.length === 0 ? (
              <EmptyState
                variant="no-results"
                title={loaded[scope] ? 'Inbox is empty' : 'No inbox loaded'}
                description={
                  loaded[scope]
                    ? `No conversations for ${shortUrn(committedAddress)}.`
                    : 'Enter a recipient URN above and load its inbox.'
                }
              />
            ) : (
              <ul className="divide-y divide-zinc-800/60">
                {conversations.map((c) => {
                  const active = c.correspondent === selectedCorrespondent
                  return (
                    <li key={c.correspondent}>
                      <button
                        type="button"
                        onClick={() => setSelectedCorrespondent(c.correspondent)}
                        className={cn(
                          'flex w-full flex-col gap-1 px-4 py-3 text-left transition-colors',
                          active ? 'bg-zinc-900' : 'hover:bg-zinc-900/50',
                        )}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="truncate font-mono text-xs text-zinc-200">
                            {shortUrn(c.correspondent)}
                          </span>
                          <span className="whitespace-nowrap text-[10px] text-zinc-500">
                            {formatRelativeTime(c.last.created_at)}
                          </span>
                        </div>
                        <div
                          className={cn(
                            'truncate text-xs',
                            c.unread > 0 ? 'font-medium text-zinc-100' : 'text-zinc-400',
                          )}
                        >
                          {messageSubject(c.last) || '(no subject)'}
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-[10px] text-zinc-600">
                            {c.messages.length}{' '}
                            {c.messages.length === 1 ? 'message' : 'messages'}
                          </span>
                          {c.unread > 0 && (
                            <span className="inline-flex min-w-[1.25rem] items-center justify-center rounded-full border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-200">
                              {c.unread} new
                            </span>
                          )}
                        </div>
                      </button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          {/* Conversation thread / detail */}
          <div className="flex flex-col overflow-auto">
            {selectedConversation ? (
              <ConversationPanel
                key={selectedConversation.correspondent}
                correspondent={selectedConversation.correspondent}
                self={committedAddress}
                messages={selectedConversation.messages}
                onClose={() => setSelectedCorrespondent(null)}
                onChanged={(envs) => mergeIntoScope(scope, envs)}
              />
            ) : (
              <div className="flex h-full items-center justify-center p-6 text-center text-sm text-zinc-500">
                Select a conversation to view its messages and reply.
              </div>
            )}
          </div>
        </div>
      </div>

      <ComposeMessageDialog
        open={composeOpen}
        onOpenChange={setComposeOpen}
        defaultFrom={committedAddress}
        onSent={(env) => mergeIntoScope(scope, [env])}
      />
    </div>
  )
}

interface ConversationPanelProps {
  /** Correspondent URN — the conversation's group key. */
  correspondent: string
  /** The operator's committed inbox address — distinguishes own vs. their messages. */
  self: string
  /** This conversation's in-memory messages (parent panel order, newest-first). */
  messages: MessageEnvelope[]
  onClose: () => void
  onChanged: (envs: MessageEnvelope[]) => void
}

function ConversationPanel({
  correspondent,
  self,
  messages,
  onClose,
  onChanged,
}: ConversationPanelProps) {
  const api = useApi()
  const selfTrim = self.trim()

  const [reply, setReply] = useState('')
  const [sending, setSending] = useState(false)
  const [cancelingId, setCancelingId] = useState<string | null>(null)
  const [canceledIds, setCanceledIds] = useState<Set<string>>(() => new Set())
  const [backfilling, setBackfilling] = useState(false)
  const [backfillError, setBackfillError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  // Messages oldest-first for chat-style rendering. Inbound + outbound + any
  // backfilled history all flow in through the `messages` prop, so SSE and
  // sends stay live without separate panel state.
  const displayed = useMemo(
    () =>
      [...messages].sort(
        (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      ),
    [messages],
  )
  const latest = displayed.length > 0 ? displayed[displayed.length - 1] : null

  // Backfill: `getInbox` only drains messages addressed TO the operator, so a
  // conversation's own outbound replies are missing after a reload. Fetch each
  // distinct thread the conversation touches and merge the union back up. This
  // is best-effort — failures are surfaced but non-blocking.
  useEffect(() => {
    let cancelled = false
    // Captured from the mount render — the panel is keyed by correspondent,
    // so a new conversation remounts it with its own initial message set.
    const tids = backfillThreadKeys(messages)
    if (tids.length === 0) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- backfill status reflects the effect-owned async request.
    setBackfilling(true)
    setBackfillError(null)
    ;(async () => {
      try {
        const results = await Promise.all(tids.map((tid) => api.getThread(tid)))
        if (cancelled) return
        const union = results.flat()
        if (union.length > 0) onChanged(union)
      } catch (err) {
        if (!cancelled) {
          setBackfillError(
            err instanceof Error ? err.message : 'Failed to load conversation history',
          )
        }
      } finally {
        if (!cancelled) setBackfilling(false)
      }
    })()
    return () => {
      cancelled = true
    }
    // Runs once per correspondent — the panel is keyed by correspondent, so a
    // new conversation remounts it. `messages` and `onChanged` are read from
    // the mount closure and intentionally excluded from deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [correspondent])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'nearest' })
  }, [displayed.length])

  async function sendReply() {
    if (!reply.trim() || !latest) return
    setSending(true)
    try {
      const env = await api.sendMessage({
        kind: 'response',
        from: self,
        to: correspondent,
        in_reply_to: latest.id,
        thread_id: threadKey(latest),
        payload: { body: reply.trim() },
        content_type: 'application/json',
      })
      setReply('')
      notifySuccess('Reply sent')
      onChanged([env])
    } catch (err) {
      notifyError(err, 'Failed to send reply')
    } finally {
      setSending(false)
    }
  }

  async function cancelMessage(m: MessageEnvelope) {
    setCancelingId(m.id)
    try {
      await api.cancelMessage(m.id)
      notifySuccess('Message canceled')
      setCanceledIds((prev) => new Set(prev).add(m.id))
    } catch (err) {
      notifyError(err, 'Failed to cancel message')
    } finally {
      setCancelingId(null)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-start justify-between gap-3 border-b border-zinc-800/80 px-6 py-4">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="truncate text-sm font-medium text-zinc-100">
            {shortUrn(correspondent)}
          </h2>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-zinc-500">
            <span>
              {displayed.length} {displayed.length === 1 ? 'message' : 'messages'}
            </span>
            {backfilling && <span className="text-zinc-600">loading history…</span>}
            <CopyableId id={correspondent} />
          </div>
        </div>
        <Button
          size="sm"
          variant="ghost"
          onClick={onClose}
          aria-label="Close conversation"
          className="h-8 w-8 p-0"
        >
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div className="flex flex-1 flex-col gap-3 overflow-auto p-6">
        {backfillError && (
          <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-200/80">
            Couldn't load full history: {backfillError}
          </div>
        )}

        {displayed.length === 0 ? (
          <Skeleton className="h-24 w-full rounded-md" />
        ) : (
          displayed.map((m) => {
            const isSelf = m.from.trim() === selfTrim
            const status: DisplayMessageStatus = canceledIds.has(m.id)
              ? 'canceled'
              : messageStatus(m)
            const linked = sourceTaskId(m)
            const details = extraPayloadFields(m)
            return (
              <div
                key={m.id}
                className={cn('flex flex-col', isSelf ? 'items-end' : 'items-start')}
              >
                <div
                  className={cn(
                    'max-w-[85%] rounded-md border px-4 py-3',
                    isSelf
                      ? 'border-zinc-700 bg-zinc-900'
                      : 'border-zinc-800/80 bg-zinc-950',
                  )}
                >
                  <div className="mb-1.5 flex flex-wrap items-center justify-between gap-2 text-[11px] text-zinc-500">
                    <span className="font-mono">{shortUrn(m.from)}</span>
                    <span className="flex items-center gap-2">
                      <MessageStatusBadge status={status} />
                      <span>{new Date(m.created_at).toLocaleString()}</span>
                    </span>
                  </div>
                  <div className="whitespace-pre-wrap break-words text-[13px] leading-6 text-zinc-200">
                    {messageBody(m) || '(no message text)'}
                  </div>
                  {linked && (
                    <div className="mt-2 text-[11px] text-zinc-500">
                      task{' '}
                      <Link
                        to={`/tasks/${linked}`}
                        className="font-mono text-zinc-300 underline-offset-4 hover:text-zinc-100 hover:underline"
                      >
                        {linked}
                      </Link>
                    </div>
                  )}
                  {details.length > 0 && (
                    <dl className="mt-2 grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1 border-t border-zinc-800/60 pt-2 text-[11px]">
                      {details.map(([k, v]) => (
                        <div key={k} className="contents">
                          <dt className="truncate text-zinc-500">{k}</dt>
                          <dd className="break-words font-mono text-zinc-400">
                            {typeof v === 'string' ? v : JSON.stringify(v)}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  )}
                  <div className="mt-1.5 flex items-center justify-between gap-2">
                    <CopyableId id={m.id} />
                    {status === 'pending' && (
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-6 px-2 text-[11px]"
                        onClick={() => cancelMessage(m)}
                        disabled={cancelingId === m.id}
                      >
                        {cancelingId === m.id ? 'Canceling…' : 'Cancel'}
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            )
          })
        )}

        <div ref={bottomRef} />
      </div>

      <div className="border-t border-zinc-800/80 p-4">
        <Textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          placeholder={`Reply to ${shortUrn(correspondent)}…`}
          rows={3}
          className="w-full"
        />
        <div className="mt-2 flex items-center justify-between">
          <span className="text-[11px] text-zinc-600">
            Sends a <span className="text-zinc-500">response</span> to{' '}
            <span className="font-mono text-zinc-500">{shortUrn(correspondent)}</span>.
          </span>
          <Button size="sm" onClick={sendReply} disabled={sending || !reply.trim() || !latest}>
            <Send className="h-3.5 w-3.5" />
            {sending ? 'Sending…' : 'Send reply'}
          </Button>
        </div>
      </div>
    </div>
  )
}

/** Surface a Torque task id from envelope metadata, when present. */
function sourceTaskId(m: MessageEnvelope): string | null {
  const meta = m.metadata
  if (!meta) return null
  return meta.task_id || meta.taskId || null
}
