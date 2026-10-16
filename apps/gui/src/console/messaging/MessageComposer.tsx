import { useEffect, useId, useRef, useState } from 'react'
import { messageAction } from './actions'
import { useMessaging } from './context'
import { DeliveryStatus } from './DeliveryStatus'
import { isBroadcast, MAX_BODY, validateSend } from './guards'
import { RecentThread } from './RecentThread'
import { renderTemplate, templatesFor, type TemplateVars } from './templates'
import type { MessageIntent, Recipient, SendRequest, SentMessage } from './types'

const TITLE: Record<MessageIntent, string> = { message: 'Send message', nudge: 'Nudge', update_request: 'Request update' }
const SEND_LABEL: Record<MessageIntent, string> = { message: 'Send', nudge: 'Send nudge', update_request: 'Request update' }

export interface MessageComposerProps {
  /** Null/unresolvable recipients disable sending. */
  recipient: Recipient | null
  intent?: MessageIntent
  /** Continue a thread / reply to a message. */
  replyTo?: { id: string; threadId?: string }
  /** What this is about (task id, PR); fills template {on}. */
  subject?: string
  vars?: TemplateVars
  showThread?: boolean
  onSent?: (sent: SentMessage) => void
  onCancel?: () => void
  autoFocus?: boolean
}

const field = 'w-full border border-zinc-700 bg-zinc-950 px-2 py-1.5 font-mono text-xs text-zinc-100 placeholder:text-zinc-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'
const btn = 'border px-3 py-1.5 font-mono text-[11px] uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40'

export function MessageComposer({ recipient, intent = 'message', replyTo, subject, vars, showThread = true, onSent, onCancel, autoFocus = true }: MessageComposerProps) {
  const { bus, operator } = useMessaging()
  const id = useId()
  const templates = intent === 'message' ? [] : templatesFor(intent)
  const fill = (tid: string) => {
    const t = templates.find((x) => x.id === tid)
    return t ? renderTemplate(t, { ...vars, agent: recipient?.label, subject: subject ?? vars?.subject }) : ''
  }
  const [templateId, setTemplateId] = useState(templates[0]?.id ?? '')
  const [body, setBody] = useState(() => fill(templates[0]?.id ?? ''))
  const [expected, setExpected] = useState(templates[0]?.expectedResponse ?? '')
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [sent, setSent] = useState<SentMessage | null>(null)
  const [refresh, setRefresh] = useState(0)
  const area = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    if (autoFocus) area.current?.focus()
  }, [autoFocus])

  const pick = (tid: string) => {
    setTemplateId(tid)
    setBody(fill(tid))
    setExpected(templates.find((x) => x.id === tid)?.expectedResponse ?? '')
  }

  const req: SendRequest | null = recipient && {
    to: recipient,
    intent,
    body,
    templateId: templateId || undefined,
    expectedResponse: intent === 'update_request' && expected.trim() ? expected.trim() : undefined,
    threadId: replyTo?.threadId ?? replyTo?.id,
    inReplyTo: replyTo?.id,
    subject: subject || undefined,
    confirmedBroadcast: confirmed,
  }
  const problem = req ? validateSend(req) : 'No resolvable recipient. Pick an agent with a registered address.'
  const needsConfirm = !!recipient && isBroadcast(recipient)

  const submit = async () => {
    if (!req || problem || busy) return
    setBusy(true)
    setError(null)
    const res = await bus.dispatch(messageAction(req))
    setBusy(false)
    if (res.status !== 'ok') return setError(res.message ?? 'Could not send')
    const out = res.data as SentMessage
    setSent(out)
    setRefresh((n) => n + 1)
    if (intent === 'message') setBody('')
    onSent?.(out)
  }

  return (
    <form
      aria-label={TITLE[intent]}
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="font-mono text-sm text-zinc-100">{TITLE[intent]}</h2>
        <span className="truncate font-mono text-[11px] text-zinc-400">
          to {recipient ? recipient.label : <span className="text-red-300">no recipient</span>}
          {replyTo ? ' · reply' : ''}
        </span>
      </div>

      {templates.length > 0 && (
        <div role="group" aria-label="Templates" className="flex flex-wrap gap-1.5">
          {templates.map((t) => (
            <button key={t.id} type="button" aria-pressed={t.id === templateId} onClick={() => pick(t.id)} className={`${btn} ${t.id === templateId ? 'border-amber-400/60 text-amber-300' : 'border-zinc-700 text-zinc-400 hover:text-zinc-200'}`}>
              {t.label}
            </button>
          ))}
        </div>
      )}

      <div>
        <label htmlFor={`${id}-body`} className="mb-1 block font-mono text-[10px] uppercase tracking-widest text-zinc-500">Message</label>
        <textarea
          ref={area}
          id={`${id}-body`}
          rows={intent === 'message' ? 5 : 3}
          value={body}
          maxLength={MAX_BODY}
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              void submit()
            }
          }}
          placeholder="Write a message"
          className={field}
        />
      </div>

      {intent === 'update_request' && (
        <div>
          <label htmlFor={`${id}-exp`} className="mb-1 block font-mono text-[10px] uppercase tracking-widest text-zinc-500">Expected response</label>
          <input id={`${id}-exp`} value={expected} onChange={(e) => setExpected(e.target.value)} placeholder="e.g. one line, within 10 minutes" className={field} />
        </div>
      )}

      {needsConfirm && (
        <label className="flex items-center gap-2 border border-amber-400/40 px-2 py-1.5 font-mono text-xs text-amber-300">
          <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
          This reaches everyone in {recipient?.label}. Confirm broadcast.
        </label>
      )}

      {(error || (problem && body.trim())) && (
        <p role="alert" className="font-mono text-xs text-red-300">{error ?? problem}</p>
      )}

      <div className="flex items-center justify-between gap-3">
        <div className="min-h-4">{sent && <DeliveryStatus key={sent.id} messageId={sent.id} initial={sent.state} />}</div>
        <div className="flex gap-2">
          {onCancel && <button type="button" onClick={onCancel} className={`${btn} border-zinc-700 text-zinc-400 hover:text-zinc-200`}>{sent ? 'Close' : 'Cancel'}</button>}
          <button type="submit" disabled={!!problem || busy} className={`${btn} border-amber-400/60 text-amber-300 hover:bg-amber-400/10`}>{busy ? 'Sending' : SEND_LABEL[intent]}</button>
        </div>
      </div>

      {showThread && recipient && (
        <RecentThread query={{ with: recipient.address, threadId: replyTo?.threadId, limit: 4 }} refreshKey={refresh} operator={operator} />
      )}
    </form>
  )
}
