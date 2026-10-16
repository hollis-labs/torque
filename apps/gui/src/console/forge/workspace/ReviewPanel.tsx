import { useState } from 'react'
import { Btn } from '../../agents/ui'
import type { ReviewVerdict } from '../types'
import { LABEL } from './Comments'
import type { PendingComment, WriteResult } from './useWorkspace'

const OPTIONS: { value: ReviewVerdict; label: string; hint: string }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without an explicit approval.' },
  { value: 'approve', label: 'Approve', hint: 'Approve these changes.' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback must be addressed before merging.' },
]

export interface ReviewPanelProps {
  pending: PendingComment[]
  onSubmit: (v: { verdict: ReviewVerdict; body?: string }) => Promise<WriteResult>
  onUnqueue: (id: string) => void
  onClose: () => void
}

export function ReviewPanel({ pending, onSubmit, onUnqueue, onClose }: ReviewPanelProps) {
  const [verdict, setVerdict] = useState<ReviewVerdict>('comment')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const needsText = verdict === 'request_changes' && !body.trim() && pending.length === 0
  const emptyComment = verdict === 'comment' && !body.trim() && pending.length === 0
  const blocked = needsText || emptyComment

  const submit = async () => {
    setBusy(true)
    setError(null)
    const r = await onSubmit({ verdict, body: body.trim() || undefined })
    setBusy(false)
    if (r.ok) onClose()
    else setError(r.message ?? 'Review not submitted')
  }

  return (
    <section className="border-b border-amber-300/40 bg-zinc-900/80 px-4 py-3" aria-label="Submit review" data-testid="forge-review-panel">
      <h2 className={`${LABEL} mb-2 text-amber-300`}>Finish your review</h2>
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={3}
        placeholder="Leave a review summary…"
        aria-label="Review summary"
        disabled={busy}
        className="mb-3 w-full max-w-3xl resize-y border border-zinc-700 bg-zinc-950 px-2 py-1.5 text-[13px] text-zinc-100 placeholder:text-zinc-600 focus-visible:border-amber-300 focus-visible:outline-none"
      />
      <fieldset className="mb-3 space-y-1.5" disabled={busy}>
        <legend className="sr-only">Review verdict</legend>
        {OPTIONS.map((o) => (
          <label key={o.value} className="flex cursor-pointer items-start gap-2 text-[13px]">
            <input type="radio" name="verdict" value={o.value} checked={verdict === o.value} onChange={() => setVerdict(o.value)} className="mt-1 accent-amber-300" />
            <span>
              <span className="text-zinc-100">{o.label}</span>
              <span className="ml-2 text-[12px] text-zinc-500">{o.hint}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {pending.length > 0 && (
        <div className="mb-3 max-w-3xl">
          <p className={`${LABEL} mb-1`}>{pending.length} pending inline comment{pending.length === 1 ? '' : 's'}</p>
          <ul className="divide-y divide-zinc-800/60 border-y border-zinc-800/60">
            {pending.map((p) => (
              <li key={p.id} className="flex items-baseline gap-2 py-1.5 text-[12px]">
                <span className="shrink-0 font-mono text-zinc-500">{p.path}:{p.line}</span>
                <span className="min-w-0 flex-1 truncate text-zinc-300">{p.body}</span>
                <button type="button" onClick={() => onUnqueue(p.id)} className="font-mono text-[10px] uppercase tracking-wider text-zinc-500 hover:text-red-300">Discard</button>
              </li>
            ))}
          </ul>
        </div>
      )}
      {needsText && <p className="mb-2 text-[12px] text-amber-300">Requesting changes needs a summary or at least one inline comment.</p>}
      {error && <p className="mb-2 text-[12px] text-red-300" role="alert">{error}</p>}
      <div className="flex items-center gap-2">
        <span className="mr-auto font-mono text-[10px] text-zinc-600">You will be asked to confirm your identity.</span>
        <Btn onClick={onClose} disabled={busy}>Cancel</Btn>
        <Btn tone="primary" onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Submitting…' : 'Submit review'}</Btn>
      </div>
    </section>
  )
}
