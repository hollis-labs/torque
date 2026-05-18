import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Bot, Inbox, RefreshCw, Send, User, X } from 'lucide-react'
import { PageHeader } from '@/components/domain/page-header'
import { SummaryCards } from '@/components/domain/summary-cards'
import { EmptyState } from '@/components/domain/empty-state'
import { CopyableId } from '@/components/domain/copyable-id'
import { ComposeMessageDialog } from '@/components/domain/compose-message-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import { notifyError, notifySuccess } from '@/lib/toast'
import { cn } from '@/lib/utils'
import {
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

const STATUS_STYLES: Record<MessageStatus, string> = {
  pending: 'border-amber-500/40 bg-amber-500/10 text-amber-200',
  delivered: 'border-blue-500/40 bg-blue-500/10 text-blue-200',
  consumed: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200',
}

function MessageStatusBadge({ status }: { status: MessageStatus }) {
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
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [sseStatus, setSseStatus] = useState<'idle' | 'connecting' | 'live' | 'error'>('idle')
  const [composeOpen, setComposeOpen] = useState(false)

  const committedAddress = addresses[scope]
  const messages = panels[scope]
  const scopeMeta = SCOPES.find((s) => s.key === scope)!

  // Keep the address input in sync when switching tabs.
  useEffect(() => {
    setDraft(addresses[scope])
    setSelectedId(null)
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

  const selected = useMemo(
    () => messages.find((m) => m.id === selectedId) ?? null,
    [messages, selectedId],
  )

  const counts = useMemo(() => {
    const pending = messages.filter((m) => messageStatus(m) === 'pending').length
    const threads = new Set(messages.map((m) => threadKey(m))).size
    return { pending, threads }
  }, [messages])

  const summaryCards = [
    { label: scopeMeta.label, value: messages.length, subtitle: 'messages' },
    { label: 'Pending', value: counts.pending, accentColor: '#f59e0b' },
    { label: 'Threads', value: counts.threads, accentColor: '#60a5fa' },
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
          {/* Inbox list */}
          <div className="flex flex-col overflow-auto border-r border-zinc-800/80">
            {loading && messages.length === 0 ? (
              <ListSkeleton />
            ) : error ? (
              <EmptyState
                variant="error"
                description={error}
                action={{
                  label: 'Retry',
                  onClick: () => loadInbox(committedAddress, scope),
                }}
              />
            ) : messages.length === 0 ? (
              <EmptyState
                variant="no-results"
                title={loaded[scope] ? 'Inbox is empty' : 'No inbox loaded'}
                description={
                  loaded[scope]
                    ? `No messages addressed to ${shortUrn(committedAddress)}.`
                    : 'Enter a recipient URN above and load its inbox.'
                }
              />
            ) : (
              <ul className="divide-y divide-zinc-800/60">
                {messages.map((m) => {
                  const active = m.id === selectedId
                  const status = messageStatus(m)
                  return (
                    <li key={m.id}>
                      <button
                        type="button"
                        onClick={() => setSelectedId(m.id)}
                        className={cn(
                          'flex w-full flex-col gap-1 px-4 py-3 text-left transition-colors',
                          active ? 'bg-zinc-900' : 'hover:bg-zinc-900/50',
                        )}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-[10px] uppercase tracking-wider text-zinc-500">
                            {m.kind}
                          </span>
                          <span className="whitespace-nowrap text-[10px] text-zinc-500">
                            {formatRelativeTime(m.created_at)}
                          </span>
                        </div>
                        <div
                          className={cn(
                            'truncate text-xs',
                            status === 'pending'
                              ? 'font-medium text-zinc-100'
                              : 'text-zinc-300',
                          )}
                        >
                          {messageSubject(m) || '(no subject)'}
                        </div>
                        <div className="flex items-center justify-between gap-2">
                          <span className="truncate font-mono text-[10px] text-zinc-600">
                            {shortUrn(m.from)} → {shortUrn(m.to)}
                          </span>
                          <MessageStatusBadge status={status} />
                        </div>
                      </button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          {/* Thread / detail */}
          <div className="flex flex-col overflow-auto">
            {selected ? (
              <ThreadPanel
                key={selected.id}
                message={selected}
                onClose={() => setSelectedId(null)}
                onChanged={(envs) => mergeIntoScope(scope, envs)}
              />
            ) : (
              <div className="flex h-full items-center justify-center p-6 text-center text-sm text-zinc-500">
                Select a message to view its thread and reply.
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

interface ThreadPanelProps {
  message: MessageEnvelope
  onClose: () => void
  onChanged: (envs: MessageEnvelope[]) => void
}

function ThreadPanel({ message, onClose, onChanged }: ThreadPanelProps) {
  const api = useApi()
  const tid = threadKey(message)

  const [thread, setThread] = useState<MessageEnvelope[]>([message])
  const [loading, setLoading] = useState(true)
  const [threadError, setThreadError] = useState<string | null>(null)
  const [reply, setReply] = useState('')
  const [sending, setSending] = useState(false)
  const [canceling, setCanceling] = useState(false)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const fetchThread = useCallback(async () => {
    setLoading(true)
    setThreadError(null)
    try {
      const envs = await api.getThread(tid)
      const merged = envs.length > 0 ? envs : [message]
      merged.sort(
        (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      )
      setThread(merged)
      onChanged(envs)
    } catch (err) {
      setThreadError(err instanceof Error ? err.message : 'Failed to load thread')
    } finally {
      setLoading(false)
    }
    // onChanged is stable enough; message is keyed by the parent via `key`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, tid])

  useEffect(() => {
    fetchThread()
  }, [fetchThread])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'nearest' })
  }, [thread.length])

  async function sendReply() {
    if (!reply.trim()) return
    setSending(true)
    try {
      const env = await api.sendMessage({
        kind: 'response',
        from: message.to,
        to: message.from,
        in_reply_to: message.id,
        thread_id: tid,
        payload: { body: reply.trim() },
        content_type: 'application/json',
      })
      setReply('')
      notifySuccess('Reply sent')
      onChanged([env])
      fetchThread()
    } catch (err) {
      notifyError(err, 'Failed to send reply')
    } finally {
      setSending(false)
    }
  }

  async function cancelMessage() {
    setCanceling(true)
    try {
      await api.cancelMessage(message.id)
      notifySuccess('Message canceled')
      fetchThread()
    } catch (err) {
      notifyError(err, 'Failed to cancel message')
    } finally {
      setCanceling(false)
    }
  }

  const details = extraPayloadFields(message)
  const canCancel = messageStatus(message) === 'pending'

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-start justify-between gap-3 border-b border-zinc-800/80 px-6 py-4">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="truncate text-sm font-medium text-zinc-100">
            {messageSubject(message) || `${message.kind} message`}
          </h2>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-zinc-500">
            <span className="font-mono">
              {shortUrn(message.from)} → {shortUrn(message.to)}
            </span>
            <span className="uppercase tracking-wider">{message.kind}</span>
            <CopyableId id={message.id} />
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {canCancel && (
            <Button size="sm" variant="outline" onClick={cancelMessage} disabled={canceling}>
              {canceling ? 'Canceling…' : 'Cancel'}
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={onClose}
            aria-label="Close thread"
            className="h-8 w-8 p-0"
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      </div>

      <div className="flex flex-1 flex-col gap-3 overflow-auto p-6">
        {loading ? (
          <Skeleton className="h-24 w-full rounded-md" />
        ) : threadError ? (
          <EmptyState
            variant="error"
            description={threadError}
            action={{ label: 'Retry', onClick: fetchThread }}
          />
        ) : (
          thread.map((m) => {
            const linked = sourceTaskId(m)
            return (
              <div
                key={m.id}
                className={cn(
                  'rounded-md border px-4 py-3',
                  m.id === message.id
                    ? 'border-zinc-700 bg-zinc-900'
                    : 'border-zinc-800/80 bg-zinc-950',
                )}
              >
                <div className="mb-1.5 flex flex-wrap items-center justify-between gap-2 text-[11px] text-zinc-500">
                  <span className="font-mono">{shortUrn(m.from)}</span>
                  <span>{new Date(m.created_at).toLocaleString()}</span>
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
              </div>
            )
          })
        )}

        {details.length > 0 && (
          <div className="rounded-md border border-zinc-800/80 bg-zinc-950 px-4 py-3">
            <p className="mb-2 text-[10px] uppercase tracking-wider text-zinc-500">
              Payload details
            </p>
            <dl className="grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 gap-y-1.5 text-[12px]">
              {details.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="truncate text-zinc-500">{k}</dt>
                  <dd className="break-words font-mono text-zinc-300">
                    {typeof v === 'string' ? v : JSON.stringify(v)}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        )}

        <div ref={bottomRef} />
      </div>

      <div className="border-t border-zinc-800/80 p-4">
        <Textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          placeholder={`Reply to ${shortUrn(message.from)}…`}
          rows={3}
          className="w-full"
        />
        <div className="mt-2 flex items-center justify-between">
          <span className="text-[11px] text-zinc-600">
            Sends a <span className="text-zinc-500">response</span> in this thread.
          </span>
          <Button size="sm" onClick={sendReply} disabled={sending || !reply.trim()}>
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
