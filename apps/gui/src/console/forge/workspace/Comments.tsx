import { useState, type ReactNode } from 'react'
import { ago } from '@/lib/agent-activity'
import { Linkified } from '../../autolink/Linkified'
import { Btn } from '../../agents/ui'
import type { ForgeComment, ForgeUser, ReviewComment } from '../types'
import type { Thread } from './diff-utils'

export const LABEL = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'

export function Badge({ children, tone = 'border-zinc-700 text-zinc-400' }: { children: ReactNode; tone?: string }) {
  return <span className={`inline-flex shrink-0 items-center border px-1.5 py-px font-mono text-[10px] uppercase tracking-wider ${tone}`}>{children}</span>
}

export function Author({ user }: { user: ForgeUser }) {
  return (
    <span className="font-mono text-[12px] text-zinc-200">
      @{user.login}
      {user.isAgent && <span className="ml-1 text-zinc-500">· agent</span>}
    </span>
  )
}

export function CommentItem({ c, posting, onReply }: { c: ForgeComment; posting?: boolean; onReply?: () => void }) {
  const [now] = useState(() => Date.now())
  return (
    <article className={`px-3 py-2 ${posting ? 'opacity-60' : ''}`} data-testid="forge-comment" aria-busy={posting || undefined}>
      <header className="flex flex-wrap items-center gap-2">
        <Author user={c.author} />
        <span className="font-mono text-[10px] text-zinc-500">{posting ? 'posting…' : ago(Date.parse(c.createdAt), now)}</span>
        {onReply && !posting && (
          <button type="button" onClick={onReply} className="ml-auto font-mono text-[10px] uppercase tracking-wider text-zinc-500 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
            Reply
          </button>
        )}
      </header>
      {/* Body is untrusted forge text: rendered as text only, with ref chips. */}
      <div className="mt-1 text-[13px] leading-relaxed text-zinc-300 [overflow-wrap:anywhere]">
        <Linkified text={c.body} preserveWhitespace />
      </div>
    </article>
  )
}

export interface ComposerProps {
  placeholder: string
  submitLabel: string
  /** Extra secondary action (e.g. "Add to review"). Clears the draft on call. */
  secondary?: { label: string; run: (body: string) => void }
  onSubmit: (body: string) => Promise<{ ok: boolean; message?: string }>
  onCancel?: () => void
  autoFocus?: boolean
  disabled?: boolean
}

/** Draft is kept until the write succeeds, so a failed post never loses the text. */
export function Composer({ placeholder, submitLabel, secondary, onSubmit, onCancel, autoFocus, disabled }: ComposerProps) {
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const empty = !body.trim()
  const submit = async () => {
    if (empty || busy) return
    setBusy(true)
    setError(null)
    const res = await onSubmit(body)
    setBusy(false)
    if (res.ok) setBody('')
    else setError(res.message ?? 'Could not post')
  }
  return (
    <div className="space-y-2">
      <textarea
        value={body}
        autoFocus={autoFocus}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void submit()
          if (e.key === 'Escape' && onCancel) onCancel()
        }}
        rows={3}
        placeholder={placeholder}
        aria-label={placeholder}
        disabled={busy || disabled}
        className="w-full resize-y border border-zinc-700 bg-zinc-950 px-2 py-1.5 font-sans text-[13px] text-zinc-100 placeholder:text-zinc-500 focus-visible:border-amber-300 focus-visible:outline-none disabled:opacity-60"
      />
      {error && <p className="text-[12px] text-red-300" role="alert">{error}. Your draft is kept; try again.</p>}
      <div className="flex items-center gap-2">
        <span className="mr-auto font-mono text-[10px] text-zinc-500">Ctrl+Enter to post</span>
        {onCancel && <Btn onClick={onCancel} disabled={busy}>Cancel</Btn>}
        {secondary && <Btn onClick={() => { if (!empty) { secondary.run(body); setBody('') } }} disabled={empty || busy || disabled}>{secondary.label}</Btn>}
        <Btn tone="primary" onClick={() => void submit()} disabled={empty || busy || disabled}>{busy ? 'Posting…' : submitLabel}</Btn>
      </div>
    </div>
  )
}

export interface ThreadViewProps {
  thread: Thread
  posting: ForgeComment[]
  readOnly: boolean
  onReply: (inReplyTo: string, body: string) => Promise<{ ok: boolean; message?: string }>
  showPath?: boolean
}

export function ThreadView({ thread, posting, readOnly, onReply, showPath }: ThreadViewProps) {
  const [replying, setReplying] = useState(false)
  const [open, setOpen] = useState(!(thread.outdated || thread.resolved))
  const mine = posting.filter((p): p is ReviewComment => p.kind === 'review' && p.inReplyTo === thread.id)
  const loc = thread.root.line != null ? `${showPath ? `${thread.path}:` : 'line '}${thread.root.line}${thread.root.side === 'left' ? ' (old)' : ''}` : thread.path
  return (
    <section className="border-y border-zinc-700/80 bg-zinc-900/70" data-testid="forge-thread" aria-label={`Comment thread on ${loc}`}>
      <header className="flex flex-wrap items-center gap-2 px-3 py-1.5">
        <span className="font-mono text-[10px] text-zinc-500">{loc}</span>
        {thread.outdated && <Badge tone="border-amber-500/50 text-amber-300">Outdated</Badge>}
        {thread.resolved && <Badge tone="border-emerald-500/50 text-emerald-300">Resolved</Badge>}
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="ml-auto font-mono text-[10px] uppercase tracking-wider text-zinc-500 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
        >
          {open ? 'Collapse' : `Show ${thread.replies.length + 1}`}
        </button>
      </header>
      {open && (
        <>
          <div className="divide-y divide-zinc-800/60">
            <CommentItem c={thread.root} />
            {thread.replies.map((r) => <CommentItem key={r.id} c={r} />)}
            {mine.map((p) => <CommentItem key={p.id} c={p} posting />)}
          </div>
          {!readOnly && (
            <div className="border-t border-zinc-800/60 px-3 py-2">
              {replying ? (
                <Composer
                  autoFocus
                  placeholder="Reply…"
                  submitLabel="Reply"
                  onCancel={() => setReplying(false)}
                  onSubmit={async (b) => {
                    const r = await onReply(thread.id, b)
                    if (r.ok) setReplying(false)
                    return r
                  }}
                />
              ) : (
                <button type="button" onClick={() => setReplying(true)} className="font-mono text-[10px] uppercase tracking-wider text-zinc-500 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
                  Reply
                </button>
              )}
            </div>
          )}
        </>
      )}
    </section>
  )
}
