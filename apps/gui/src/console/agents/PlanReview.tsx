import type { ReactNode } from 'react'
import { Block, Btn, Chip, RiskBadge, StatusBadge } from './ui'
import { clock, RISK_STYLE } from './ui-util'
import type { LifecycleItem, RetireImpact } from './lifecycle'
import type { AgentPlan, ApplyOutcome, FieldChange, FieldValue, ValidationIssue } from './plan'

const show = (v: FieldValue) => (typeof v === 'boolean' ? (v ? 'on' : 'off') : v === '' ? '(empty)' : String(v))

function ChangeRow({ c }: { c: FieldChange }) {
  return (
    <li className="border-l-2 border-amber-300/70 bg-zinc-900/40 px-3 py-2">
      <div className="font-mono text-[11px] uppercase tracking-wider text-zinc-400">{c.label}</div>
      {c.kind === 'list' ? (
        <div className="mt-1 flex flex-wrap gap-1.5">
          {c.added?.map((x) => <Chip key={`+${x}`} tone="border-emerald-400/50 text-emerald-300">+ {x}</Chip>)}
          {c.removed?.map((x) => <Chip key={`-${x}`} tone="border-rose-400/50 text-rose-300 line-through">- {x}</Chip>)}
        </div>
      ) : (
        <div className="mt-1 grid gap-1 text-[13px] sm:grid-cols-2">
          <div className="min-w-0 break-words border border-rose-400/30 bg-rose-400/5 px-2 py-1 font-mono text-xs text-rose-200"><span aria-hidden>- </span>{show(c.before)}</div>
          <div className="min-w-0 break-words border border-emerald-400/30 bg-emerald-400/5 px-2 py-1 font-mono text-xs text-emerald-200"><span aria-hidden>+ </span>{show(c.after)}</div>
        </div>
      )}
    </li>
  )
}

export function IssueList({ issues }: { issues: ValidationIssue[] }) {
  if (!issues.length) return <p className="text-[13px] text-emerald-300">No validation problems.</p>
  return (
    <ul className="space-y-1">
      {issues.map((i, n) => (
        <li key={n} className={`flex gap-2 text-[13px] ${i.level === 'error' ? 'text-rose-300' : 'text-amber-300'}`}>
          <span className="w-14 shrink-0 font-mono text-[10px] uppercase tracking-widest">{i.level}</span>
          <span>{i.message}</span>
        </li>
      ))}
    </ul>
  )
}

const ITEM_TONE: Record<LifecycleItem['tone'], { mark: string; cls: string }> = {
  add: { mark: '+', cls: 'border-emerald-400/40 bg-emerald-400/5 text-emerald-200' },
  remove: { mark: '-', cls: 'border-rose-400/40 bg-rose-400/5 text-rose-200' },
  change: { mark: '~', cls: 'border-amber-400/40 bg-amber-400/5 text-amber-200' },
  note: { mark: 'i', cls: 'border-zinc-700 bg-zinc-900/40 text-zinc-300' },
}

/** Impact counts for a retire; shared by the plan review and the retire panel. */
export function ImpactSummary({ impact }: { impact: RetireImpact }) {
  const cells: [string, number][] = [['sessions', impact.sessions.count], ['tasks', impact.tasks.length], ['workstreams', impact.workstreams.length], ['teams', impact.teams.length]]
  return (
    <dl aria-label="Impact" className="grid grid-cols-2 gap-px border border-zinc-800 bg-zinc-800 sm:grid-cols-4">
      {cells.map(([k, n]) => (
        <div key={k} className="bg-zinc-950 px-3 py-2">
          <dd className={`font-mono text-xl ${n > 0 ? 'text-amber-300' : 'text-zinc-500'}`}>{n}</dd>
          <dt className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">{k}</dt>
        </div>
      ))}
    </dl>
  )
}

export function PlanDiff({ plan }: { plan: AgentPlan }) {
  const lc = plan.lifecycle
  const sections = [...new Set(plan.changes.map((c) => c.section))]
  return (
    <>
      <Block title="Risk">
        {plan.risks.length === 0 ? (
          <p className="text-[13px] text-zinc-400">No privileged changes.</p>
        ) : (
          <ul className="space-y-1.5">
            {plan.risks.map((r) => (
              <li key={r.id} className="flex flex-wrap items-baseline gap-x-3 text-[13px]">
                <span className={`w-16 shrink-0 border px-1 text-center font-mono text-[10px] uppercase tracking-widest ${RISK_STYLE[r.level]}`}>{r.level}</span>
                <span className="text-zinc-100">{r.title}</span>
                <span className="text-zinc-500">{r.detail}</span>
              </li>
            ))}
          </ul>
        )}
      </Block>
      {lc && (
        <Block title={lc.summary}>
          {lc.impact && <div className="mb-3"><ImpactSummary impact={lc.impact} /></div>}
          <ul className="space-y-1.5">
            {lc.items.map((it, n) => (
              <li key={n} className={`flex gap-3 border-l-2 px-3 py-1.5 text-[13px] ${ITEM_TONE[it.tone].cls}`}>
                <span aria-hidden className="w-3 shrink-0 font-mono">{ITEM_TONE[it.tone].mark}</span>
                <span className="min-w-0 break-words"><span className="text-zinc-100">{it.label}</span>{it.detail && <span className="ml-2 text-zinc-400">{it.detail}</span>}</span>
              </li>
            ))}
          </ul>
          {plan.note && <p className="mt-3 border-t border-zinc-800/80 pt-2 text-[13px] text-zinc-400"><span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">reason </span>{plan.note}</p>}
        </Block>
      )}
      <Block title="Validation">
        <IssueList issues={plan.issues} />
      </Block>
      {(!lc || plan.changes.length > 0) && <Block title={`Changes (${plan.changes.length})`}>
        {plan.changes.length === 0 ? (
          <p className="text-[13px] text-zinc-500">Nothing differs from the current definition.</p>
        ) : (
          <div className="space-y-4">
            {sections.map((s) => (
              <div key={s}>
                <div className="mb-1 font-mono text-[10px] uppercase tracking-widest text-zinc-600">{s}</div>
                <ul className="space-y-1.5">{plan.changes.filter((c) => c.section === s).map((c) => <ChangeRow key={c.field} c={c} />)}</ul>
              </div>
            ))}
          </div>
        )}
        {plan.note && !lc && <p className="mt-3 border-t border-zinc-800/80 pt-2 text-[13px] text-zinc-400"><span className="font-mono text-[10px] uppercase tracking-widest text-zinc-500">reason </span>{plan.note}</p>}
      </Block>}
    </>
  )
}

export interface PlanReviewProps {
  plan: AgentPlan
  outcome: ApplyOutcome | null
  busy: boolean
  message: string | null
  onBack: () => void
  onApprove: () => void
  onApply: () => void
  onRollback: () => void
  onDone: () => void
}

export function PlanReview({ plan, outcome, busy, message, onBack, onApprove, onApply, onRollback, onDone }: PlanReviewProps) {
  const done = ['applied', 'failed', 'conflict', 'expired', 'rolled_back'].includes(plan.status)
  let banner: ReactNode = null
  if (done) {
    const ok = plan.status === 'applied' || plan.status === 'rolled_back'
    const text =
      plan.status === 'applied' ? 'Changes applied.'
      : plan.status === 'rolled_back' ? 'Changes rolled back. The previous state is restored.'
      : plan.status === 'conflict' ? 'Not applied: the definition changed since this plan was made.'
      : plan.status === 'expired' ? 'Not applied: the approval expired.'
      : 'Apply failed. Nothing changed.'
    banner = (
      <div role="status" className={`border-b px-5 py-3 text-[13px] ${ok ? 'border-emerald-400/30 bg-emerald-400/5 text-emerald-200' : 'border-rose-400/30 bg-rose-400/5 text-rose-200'}`}>
        <div className="font-medium">{text}</div>
        {outcome?.message && <div className="mt-0.5 text-zinc-300">{outcome.message}</div>}
        {outcome?.revisionAfter && <div className="mt-0.5 font-mono text-xs text-zinc-400">revision {outcome.revisionBefore || 'unknown'} → {outcome.revisionAfter}{outcome.auditId ? ` · audit ${outcome.auditId}` : ''}</div>}
      </div>
    )
  }
  return (
    <div aria-label="Change plan">
      <div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-5 py-3">
        <span className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">plan {plan.id}</span>
        <StatusBadge status={plan.status} />
        <RiskBadge risk={plan.risk} />
        <span className="ml-auto font-mono text-[11px] text-zinc-500">base {plan.baseRevision || 'unknown'} · {clock(plan.createdAt)}</span>
      </div>
      {banner}
      {message && !done && <div role="alert" className="border-b border-rose-400/30 bg-rose-400/5 px-5 py-2 text-[13px] text-rose-200">{message}</div>}
      {plan.approval && plan.status === 'approved' && (
        <div className="border-b border-emerald-400/30 bg-emerald-400/5 px-5 py-2 text-[13px] text-emerald-200">
          Approved by {plan.approval.by.label ?? plan.approval.by.id} at {clock(plan.approval.at)}. Apply before {clock(plan.approval.expiresAt)}.
        </div>
      )}
      <PlanDiff plan={plan} />
      <div className="flex flex-wrap items-center gap-2 px-5 py-3">
        {plan.status === 'proposed' && (<><Btn onClick={onBack} disabled={busy}>Back to edit</Btn><Btn tone="primary" onClick={onApprove} disabled={busy}>Approve…</Btn></>)}
        {plan.status === 'invalid' && <Btn onClick={onBack}>Back to edit</Btn>}
        {plan.status === 'approved' && (<><Btn onClick={onBack} disabled={busy}>Back to edit</Btn><Btn tone="primary" onClick={onApply} disabled={busy}>Apply…</Btn></>)}
        {plan.status === 'applied' && (<><Btn tone="danger" onClick={onRollback} disabled={busy}>Roll back…</Btn><Btn tone="primary" onClick={onDone}>Done</Btn></>)}
        {(plan.status === 'failed' || plan.status === 'conflict' || plan.status === 'expired') && (<><Btn onClick={onBack}>Re-plan</Btn><Btn onClick={onDone}>Close</Btn></>)}
        {plan.status === 'rolled_back' && <Btn tone="primary" onClick={onDone}>Done</Btn>}
        {busy && <span className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">working…</span>}
      </div>
    </div>
  )
}
