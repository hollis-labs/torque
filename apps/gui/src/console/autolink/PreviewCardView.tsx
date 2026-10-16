import { useState, type ReactNode } from 'react'
import { AlertTriangle, Check, Copy, ExternalLink, Loader2, RefreshCw, SearchX } from 'lucide-react'
import { ago } from '@/lib/agent-activity'
import type { PreviewCard, RefIdentity } from '../refs'
import { useAutolink } from './context'
import { refActions, type RefAction } from './extensions'
import { RefIcon } from './RefIcon'

import { TONE } from './tone'

const LABEL = 'font-mono text-[10px] uppercase tracking-widest text-zinc-500'
const BTN = 'inline-flex items-center gap-1.5 border border-zinc-700 px-2 py-1 font-mono text-[10px] uppercase tracking-wider text-zinc-300 transition-colors hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300'

export function StatusPill({ status }: { status: NonNullable<PreviewCard['status']> }) {
  return <span className={`inline-flex items-center border px-1.5 py-px font-mono text-[10px] uppercase tracking-wider ${TONE[status.tone]}`}>{status.label}</span>
}

function ActionControl({ action }: { action: RefAction }) {
  const { navigate } = useAutolink()
  const [copied, setCopied] = useState(false)
  if (action.type === 'copy') {
    return (
      <button
        type="button"
        className={BTN}
        onClick={() => {
          void navigator.clipboard?.writeText(action.text).then(() => {
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          })
        }}
      >
        {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
        {copied ? 'Copied' : action.label}
      </button>
    )
  }
  const t = action.target
  if (t.type === 'external') {
    return (
      <a className={BTN} href={t.href} target="_blank" rel="noopener noreferrer">
        <ExternalLink className="h-3 w-3" />
        {action.label}
      </a>
    )
  }
  return (
    <a
      className={BTN}
      href={t.to}
      onClick={(e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return
        e.preventDefault()
        navigate(t.to)
      }}
    >
      <ExternalLink className="h-3 w-3" />
      {action.label}
    </a>
  )
}

export interface PreviewCardViewProps {
  card: PreviewCard
  /** `hover` is compact (clamped description); `drawer` shows everything. */
  variant?: 'hover' | 'drawer'
  onRetry?: () => void
  /** Fixed clock for tests and stories. */
  now?: number
  /** Replaces the default field/description body (drawer detail renderers). */
  children?: ReactNode
}

/** The presentational card shared by the hovercard and the drawer. Pure props in, markup out. */
export function PreviewCardView({ card, variant = 'hover', onRetry, now, children }: PreviewCardViewProps) {
  const [clock] = useState(() => Date.now())
  const ref: RefIdentity = { kind: card.kind, canonical: card.canonical }
  const drawer = variant === 'drawer'
  const updated = card.updatedAt ? Date.parse(card.updatedAt) : NaN
  const actions = card.phase === 'loading' ? [] : refActions(ref, card)

  return (
    <div className="flex min-w-0 flex-col" data-phase={card.phase} data-testid="ref-card">
      <div className="flex items-center gap-2">
        <RefIcon token={card.icon} className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
        <span className={LABEL}>{card.kindLabel}</span>
        {card.phase === 'loading' && <Loader2 className="ml-auto h-3 w-3 animate-spin text-zinc-500" aria-label="Loading" />}
        {card.status && <span className="ml-auto"><StatusPill status={card.status} /></span>}
      </div>

      <div className={`mt-2 min-w-0 text-sm font-medium text-zinc-100 [overflow-wrap:anywhere] ${card.phase === 'loading' && !card.subtitle ? 'animate-pulse' : ''}`}>{card.title}</div>
      {card.subtitle && <div className="mt-0.5 text-[12px] text-zinc-400 [overflow-wrap:anywhere]">{card.subtitle}</div>}
      <div className="mt-0.5 font-mono text-[10px] text-zinc-500 [overflow-wrap:anywhere]">{card.canonical}</div>

      {card.phase === 'loading' && !card.subtitle && (
        <div className="mt-3 space-y-1.5" aria-hidden>
          <div className="h-2 w-3/4 animate-pulse bg-zinc-800" />
          <div className="h-2 w-1/2 animate-pulse bg-zinc-800" />
        </div>
      )}

      {card.phase === 'not-found' && (
        <p className="mt-3 flex items-start gap-2 text-[12px] text-zinc-400">
          <SearchX className="mt-px h-3.5 w-3.5 shrink-0 text-zinc-500" />
          Nothing matches this reference. It may have been deleted, or you may not have access.
        </p>
      )}

      {card.phase === 'error' && (
        <div className="mt-3 flex items-start gap-2 text-[12px] text-red-300" role="alert">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" />
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">Could not load: {card.error}</span>
          {onRetry && (
            <button type="button" className={BTN} onClick={onRetry}>
              <RefreshCw className="h-3 w-3" />
              Retry
            </button>
          )}
        </div>
      )}

      {card.phase === 'unresolved' && <p className="mt-3 text-[12px] text-zinc-500">No preview available for this reference.</p>}

      {children ?? (
        <>
          {card.description && (
            <p className={`mt-3 text-[12px] leading-relaxed text-zinc-300 [overflow-wrap:anywhere] ${drawer ? 'whitespace-pre-wrap' : 'line-clamp-3'}`}>{card.description}</p>
          )}
          {card.fields.length > 0 && (
            <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 border-t border-zinc-800/80 pt-3">
              {card.fields.map((f) => (
                <div key={f.label} className="contents">
                  <dt className={LABEL}>{f.label}</dt>
                  <dd className={`min-w-0 text-[12px] text-zinc-200 [overflow-wrap:anywhere] ${f.mono ? 'font-mono' : ''}`}>{f.value}</dd>
                </div>
              ))}
            </dl>
          )}
        </>
      )}

      {(Number.isFinite(updated) || actions.length > 0) && (
        <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-zinc-800/80 pt-3">
          {actions.map((a) => <ActionControl key={a.id} action={a} />)}
          {Number.isFinite(updated) && <span className="ml-auto font-mono text-[10px] text-zinc-500">updated {ago(updated, now ?? clock)}</span>}
        </div>
      )}
    </div>
  )
}
