// Shared card chrome: every kind renders inside CardFrame so cards look alike
// on every surface. Square, divider-based, no shadows (Torque Operations standard).
import type { ReactNode } from 'react'
import { CARD_SEVERITY_STYLE, relTime } from './frame-util'
import type { CardActionController, CardActionSpec, CardDensity, CardInstance } from './types'

const INTENT: Record<CardActionSpec['intent'], string> = {
  primary: 'border-amber-300 bg-amber-300 text-zinc-950 hover:bg-amber-200',
  neutral: 'border-zinc-700 text-zinc-200 hover:bg-zinc-800',
  danger: 'border-red-500/70 text-red-300 hover:bg-red-950/50',
}

export function CardActions({ card, controller, disabled }: { card: CardInstance; controller: CardActionController; disabled?: boolean }) {
  const settled = card.phase !== 'live'
  const last = Object.values(controller.status).find((s) => s.message && !s.pending)
  const anyPending = Object.values(controller.status).some((s) => s.pending)
  if (card.actions.length === 0 && settled) return null
  return (
    <div className="flex flex-wrap items-center gap-2 pt-2">
      {card.actions.map((a) => {
        const st = controller.status[a.id]
        return (
          <button
            key={a.id}
            type="button"
            disabled={disabled || settled || anyPending}
            onClick={() => void controller.run(a)}
            className={`border px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40 ${INTENT[a.intent]}`}
          >
            {st?.pending ? `${a.label}...` : a.label}
            {a.confirm ? <span aria-label="requires identity confirmation" className="ml-1 opacity-70">*</span> : null}
          </button>
        )
      })}
      {!settled && (
        <>
          <button type="button" disabled={anyPending} onClick={() => void controller.snooze(3_600_000)} className="px-1.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-500 hover:text-zinc-200 disabled:opacity-40">Snooze 1h</button>
          <button type="button" disabled={anyPending} onClick={() => void controller.dismiss()} className="px-1.5 py-1 font-mono text-[11px] uppercase tracking-wider text-zinc-500 hover:text-zinc-200 disabled:opacity-40">Dismiss</button>
        </>
      )}
      {last?.message && (
        <span role={last.ok ? 'status' : 'alert'} className={`font-mono text-[11px] ${last.ok ? 'text-emerald-300' : 'text-red-300'}`}>{last.message}</span>
      )}
    </div>
  )
}

export function PhaseBadge({ card }: { card: CardInstance }) {
  if (card.phase === 'live') return null
  const label = card.phase === 'resolved' ? (card.resolution?.label ?? 'Resolved') : card.phase === 'expired' ? 'Expired' : 'Superseded'
  return <span className="border border-zinc-700 px-1.5 py-px font-mono text-[10px] uppercase tracking-widest text-zinc-400">{label}</span>
}

export function CardFrame({
  card,
  density,
  kindLabel,
  children,
  footer,
  onExpand,
}: {
  card: CardInstance
  density: CardDensity
  kindLabel: string
  children?: ReactNode
  footer?: ReactNode
  onExpand?: () => void
}) {
  const sev = CARD_SEVERITY_STYLE[card.severity]
  const compact = density === 'compact'
  const dim = card.phase !== 'live'
  return (
    <article data-card-kind={card.kind} data-card-phase={card.phase} aria-label={`${kindLabel}: ${card.title}`} className={`relative flex w-full border-b border-zinc-800 ${dim ? 'opacity-70' : ''}`}>
      <span aria-hidden className={`w-0.5 shrink-0 ${dim ? 'bg-zinc-700' : sev.rail}`} />
      <div className={`min-w-0 flex-1 ${compact ? 'px-3 py-2' : 'px-4 py-3'}`}>
        <header className="flex items-center gap-2">
          <span className={`font-mono text-[10px] uppercase tracking-widest ${dim ? 'text-zinc-500' : sev.text}`}>{kindLabel}</span>
          <PhaseBadge card={card} />
          {card.count > 1 && <span className="font-mono text-[10px] text-zinc-500">x{card.count}</span>}
          <span className="ml-auto font-mono text-[10px] text-zinc-500">{relTime(card.ts)}</span>
        </header>
        {onExpand && compact ? (
          <button type="button" onClick={onExpand} className="mt-0.5 block w-full truncate text-left text-sm text-zinc-100 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">{card.title}</button>
        ) : (
          <h3 className={`mt-0.5 text-zinc-100 ${compact ? 'truncate text-sm' : 'text-sm font-medium'}`}>{card.title}</h3>
        )}
        {card.summary && <p className={`text-xs text-zinc-400 ${compact ? 'truncate' : 'mt-1'}`}>{card.summary}</p>}
        {children}
        {footer}
      </div>
    </article>
  )
}
