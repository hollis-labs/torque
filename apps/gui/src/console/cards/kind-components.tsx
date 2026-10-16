// Reference card kinds: info, approval, status (+ generic fallback).
// All strings come from agents/remote systems: rendered as text only.
import { CardActions, CardFrame } from './frame'
import type { CardRenderProps } from './types'

function subjectLine(p: CardRenderProps) {
  const subs = p.card.subjects
  if (subs.length === 0) return null
  return <p className="mt-1 truncate font-mono text-[11px] text-zinc-500">{subs.map((s) => s.label ?? s.id).join(' / ')}</p>
}

export function InfoCard(p: CardRenderProps) {
  const compact = p.density === 'compact'
  return (
    <CardFrame card={p.card} density={p.density} kindLabel="Info" onExpand={p.onExpand}>
      {!compact && subjectLine(p)}
      {!compact && <CardActions card={p.card} controller={p.controller} />}
    </CardFrame>
  )
}

export function ApprovalCard(p: CardRenderProps) {
  const { card } = p
  const payload = card.event.payload as { requestedBy?: unknown }
  const by = typeof payload.requestedBy === 'string' ? payload.requestedBy : undefined
  const gated = card.actions.some((a) => a.confirm)
  const compact = p.density === 'compact'
  return (
    <CardFrame card={card} density={p.density} kindLabel="Approval" onExpand={p.onExpand}>
      {!compact && (
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-[11px]">
          {by && (<><dt className="text-zinc-500">requested by</dt><dd className="truncate text-zinc-300">{by}</dd></>)}
          {card.expiresAt && (<><dt className="text-zinc-500">expires</dt><dd className="text-zinc-300">{new Date(card.expiresAt).toLocaleString()}</dd></>)}
          {gated && (<><dt className="text-zinc-500">confirm</dt><dd className="text-zinc-300">identity check required</dd></>)}
        </dl>
      )}
      {!compact && subjectLine(p)}
      <CardActions card={card} controller={p.controller} />
    </CardFrame>
  )
}

const STATE_DOT: Record<string, string> = { busy: 'bg-emerald-400', idle: 'bg-sky-400', waiting: 'bg-amber-400', blocked: 'bg-red-400', done: 'bg-emerald-400', review: 'bg-amber-400' }

export function StatusCard(p: CardRenderProps) {
  const { card } = p
  const state = (card.event.payload as { state?: unknown; to?: unknown })
  const key = String(state.state ?? state.to ?? '')
  const inline = p.density === 'inline'
  if (inline) {
    return (
      <span data-card-kind="status" data-card-phase={card.phase} className="inline-flex items-center gap-1.5 font-mono text-[11px] text-zinc-300">
        <span aria-hidden className={`h-1.5 w-1.5 ${STATE_DOT[key] ?? 'bg-zinc-500'}`} />
        {card.title}
        {card.summary && <span className="text-zinc-500">{card.summary}</span>}
      </span>
    )
  }
  return (
    <CardFrame card={card} density={p.density} kindLabel="Status" onExpand={p.onExpand}>
      {p.density === 'expanded' && subjectLine(p)}
      {p.density === 'expanded' && <CardActions card={card} controller={p.controller} />}
    </CardFrame>
  )
}

export function GenericCard(p: CardRenderProps) {
  return (
    <CardFrame card={p.card} density={p.density} kindLabel={p.card.event.kind} onExpand={p.onExpand}>
      {p.density !== 'compact' && <CardActions card={p.card} controller={p.controller} />}
    </CardFrame>
  )
}

