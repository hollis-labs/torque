// Shared HITL card building blocks. All strings are untrusted: text only; links
// are rendered as anchors only for http(s) URLs.
import type { ReactNode } from 'react'
import type { ConsoleRef } from '../events/types'
import type { CardActionController } from '../cards/types'
import { fmtRemaining } from './model'
import type { HitlCardState } from './use-hitl-card'
import type { HitlAuditEntry, HitlRequest, HitlRisk } from './types'

const RISK: Record<HitlRisk, string> = {
  low: 'border-zinc-700 text-zinc-400',
  medium: 'border-sky-500/60 text-sky-300',
  high: 'border-amber-400/70 text-amber-300',
  critical: 'border-red-500/70 text-red-300',
}

export function RiskBadge({ risk }: { risk: HitlRisk }) {
  return <span className={`border px-1.5 py-px font-mono text-[10px] uppercase tracking-widest ${RISK[risk]}`}>{risk} risk</span>
}

export function Countdown({ remaining }: { remaining: number | undefined }) {
  if (remaining === undefined) return null
  const urgent = remaining > 0 && remaining < 300_000
  return (
    <span data-hitl-countdown className={`font-mono text-[11px] ${remaining <= 0 ? 'text-red-300' : urgent ? 'text-amber-300' : 'text-zinc-400'}`}>
      {remaining <= 0 ? 'expired' : `expires in ${fmtRemaining(remaining)}`}
    </span>
  )
}

function safeHref(u: string | undefined): string | undefined {
  return u && /^https?:\/\//i.test(u) ? u : undefined
}

export function ContextLinks({ links }: { links: ConsoleRef[] }) {
  if (links.length === 0) return null
  return (
    <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1">
      <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">context</span>
      {links.map((l) => {
        const href = safeHref(l.url)
        const text = `${l.type}: ${l.label ?? l.id}`
        return href ? (
          <a key={`${l.type}:${l.id}`} href={href} target="_blank" rel="noreferrer noopener" className="font-mono text-[11px] text-sky-300 underline-offset-2 hover:underline">{text}</a>
        ) : (
          <span key={`${l.type}:${l.id}`} className="font-mono text-[11px] text-zinc-300">{text}</span>
        )
      })}
    </div>
  )
}

export function MetaLine({ req, remaining }: { req: HitlRequest; remaining: number | undefined }) {
  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
      <RiskBadge risk={req.risk} />
      <span className="font-mono text-[11px] text-zinc-400">from <span className="text-zinc-200">{req.requester.label ?? req.requester.id}</span></span>
      <Countdown remaining={req.status === 'pending' ? remaining : undefined} />
    </div>
  )
}

const fmtTime = (t: number) => new Date(t).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })

export function AuditTrail({ audit }: { audit: HitlAuditEntry[] }) {
  if (audit.length === 0) return null
  return (
    <details className="mt-3 border-t border-zinc-800 pt-2">
      <summary className="cursor-pointer font-mono text-[10px] uppercase tracking-widest text-zinc-500 hover:text-zinc-300">Decision audit ({audit.length})</summary>
      <ol className="mt-1.5 space-y-0.5">
        {audit.map((a, i) => (
          <li key={i} className="grid grid-cols-[8.5rem_1fr] gap-x-3 font-mono text-[11px]">
            <time className="text-zinc-500">{fmtTime(a.at)}</time>
            <span className="text-zinc-300">{a.actor} <span className="text-zinc-100">{a.action}</span>{a.note ? <span className="text-zinc-500"> - {a.note}</span> : null}</span>
          </li>
        ))}
      </ol>
    </details>
  )
}

export function StatusLine({ controller, ids }: { controller: CardActionController; ids: string[] }) {
  const last = ids.map((i) => controller.status[i]).find((s) => s && !s.pending && s.message)
  if (!last?.message) return null
  return <span role={last.ok ? 'status' : 'alert'} className={`font-mono text-[11px] ${last.ok ? 'text-emerald-300' : 'text-red-300'}`}>{last.message}</span>
}

export function ClosedNote({ s }: { s: HitlCardState }) {
  const { req, expiredLocally } = s
  if (req.status === 'answered') return <p className="mt-2 font-mono text-[11px] text-emerald-300">{req.outcome?.label ?? 'Answered'}{req.outcome?.by ? ` by ${req.outcome.by}` : ''}</p>
  if (req.status === 'withdrawn') return <p className="mt-2 font-mono text-[11px] text-zinc-400">Withdrawn by the requester</p>
  if (req.status === 'expired' || expiredLocally) return <p className="mt-2 font-mono text-[11px] text-red-300">Expired - ask the requester to send it again</p>
  return null
}

export function Btn({ intent = 'neutral', children, ...p }: { intent?: 'primary' | 'neutral' | 'danger'; children: ReactNode } & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const cls = {
    primary: 'border-amber-300 bg-amber-300 text-zinc-950 hover:bg-amber-200',
    neutral: 'border-zinc-700 text-zinc-200 hover:bg-zinc-800',
    danger: 'border-red-500/70 text-red-300 hover:bg-red-950/50',
  }[intent]
  return (
    <button type="button" {...p} className={`border px-2.5 py-1 font-mono text-[11px] uppercase tracking-wider transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40 ${cls}`}>{children}</button>
  )
}

export const FIELD = 'w-full border border-zinc-700 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder:text-zinc-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:opacity-50'
