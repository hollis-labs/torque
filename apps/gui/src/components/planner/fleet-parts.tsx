import { Mail } from 'lucide-react'
import type { FleetAgent } from '@/lib/fleet-agents'

export function Stat({ label, value, tone }: { label: string; value: string | number; tone?: string }) {
  return (
    <div className="min-w-[3.25rem]">
      <div className={`font-mono text-sm tabular-nums ${tone ?? 'text-zinc-100'}`}>{value}</div>
      <div className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">{label}</div>
    </div>
  )
}

export function Score({ value }: { value: number }) {
  return <span className="font-mono tabular-nums">{String(Math.max(0, value)).padStart(5, '0')}</span>
}

/** Unread mail for the agent. Dimmed when there is none or the source does not report mail. */
export function MailBadge({ count }: { count: number | null }) {
  const has = (count ?? 0) > 0
  return (
    <span
      className={`relative inline-flex shrink-0 ${has ? 'text-sky-300' : 'text-zinc-700'}`}
      title={count === null ? 'Messages not reported' : has ? `${count} unread message${count === 1 ? '' : 's'}` : 'No messages'}
    >
      <Mail className="h-4 w-4" aria-label={has ? `${count} unread messages` : 'No messages'} />
      {has && <span className="absolute -right-2 -top-1.5 font-mono text-[9px] leading-none text-sky-200">{count}</span>}
    </span>
  )
}

/** "claude · claude-opus-4-7": the provider, then the exact model when the source reports one. */
export function ModelTag({ agent }: { agent: FleetAgent }) {
  return (
    <span className="shrink-0 truncate font-mono text-[10px] uppercase tracking-wider text-zinc-500" title={agent.model ?? undefined}>
      {agent.provider}
      {agent.model && <span className="normal-case tracking-normal text-zinc-400"> · {agent.model}</span>}
    </span>
  )
}
