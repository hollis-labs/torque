import type { ReactNode } from 'react'
import type { Risk } from './types'
import type { PlanStatus } from './plan'

import { RISK_STYLE } from './ui-util'

export function Chip({ children, tone = 'border-zinc-700 text-zinc-300' }: { children: ReactNode; tone?: string }) {
  return <span className={`border px-1.5 py-0.5 font-mono text-[11px] ${tone}`}>{children}</span>
}

export function RiskBadge({ risk }: { risk: Risk }) {
  return <span className={`border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-widest ${RISK_STYLE[risk]}`}>{risk} risk</span>
}

const STATUS_TONE: Record<PlanStatus, string> = {
  proposed: 'border-sky-400/50 text-sky-300',
  invalid: 'border-rose-400/60 text-rose-300',
  approved: 'border-emerald-400/50 text-emerald-300',
  applied: 'border-emerald-400/50 text-emerald-300',
  failed: 'border-rose-400/60 text-rose-300',
  conflict: 'border-amber-400/50 text-amber-300',
  rolled_back: 'border-zinc-600 text-zinc-300',
  expired: 'border-amber-400/50 text-amber-300',
}

export function StatusBadge({ status }: { status: PlanStatus }) {
  return <span className={`border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-widest ${STATUS_TONE[status]}`}>{status.replace('_', ' ')}</span>
}

export function Heading({ children }: { children: ReactNode }) {
  return <h3 className="mb-2 font-mono text-[10px] uppercase tracking-widest text-zinc-500">{children}</h3>
}

export function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-b border-zinc-800/80 px-5 py-4">
      <Heading>{title}</Heading>
      {children}
    </section>
  )
}

type Tone = 'primary' | 'danger' | 'ghost'
const BTN: Record<Tone, string> = {
  primary: 'border-amber-300 bg-amber-300 text-zinc-950 hover:bg-amber-200',
  danger: 'border-rose-400/70 text-rose-200 hover:bg-rose-400/10',
  ghost: 'border-zinc-700 text-zinc-200 hover:bg-zinc-900',
}

export function Btn({ tone = 'ghost', ...p }: React.ButtonHTMLAttributes<HTMLButtonElement> & { tone?: Tone }) {
  return (
    <button
      type="button"
      {...p}
      className={`h-8 border px-3 font-mono text-[11px] uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40 ${BTN[tone]} ${p.className ?? ''}`}
    />
  )
}
