import { useEffect, useState } from 'react'
import { EmptyState } from '@hollis-labs/sysop-ui'
import { useAgentsClient } from './context'
import type { AuditEntry } from './plan'
import { Chip, RiskBadge } from './ui'
import { clock } from './ui-util'

const KIND_TONE: Record<AuditEntry['kind'], string> = {
  plan: 'border-zinc-700 text-zinc-300',
  approve: 'border-sky-400/50 text-sky-300',
  apply: 'border-emerald-400/50 text-emerald-300',
  apply_failed: 'border-rose-400/60 text-rose-300',
  rollback: 'border-amber-400/50 text-amber-300',
}

/** Per-agent change history. `version` bumps to reload after an apply. */
export function AuditTrail({ agentId, version = 0 }: { agentId: string; version?: number }) {
  const client = useAgentsClient()
  const [rows, setRows] = useState<AuditEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    client.listAudit(agentId).then((r) => active && (setRows(r), setError(null))).catch((e) => active && setError(e instanceof Error ? e.message : 'Audit unavailable'))
    return () => {
      active = false
    }
  }, [client, agentId, version])
  if (error) return <div className="p-4"><EmptyState variant="error" title="Audit trail unavailable" description={error} /></div>
  if (rows === null) return <div className="p-4 font-mono text-xs text-zinc-500">Loading audit trail…</div>
  if (rows.length === 0) return <div className="p-4"><EmptyState variant="empty" title="No changes recorded" description="Plans, approvals and applies for this agent appear here." /></div>
  return (
    <ol aria-label="Audit trail">
      {rows.map((e) => (
        <li key={e.id} className="border-b border-zinc-800/80 px-5 py-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className={`border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-widest ${KIND_TONE[e.kind]}`}>{e.kind.replace('_', ' ')}</span>
            <span className="text-[13px] text-zinc-100">{e.summary}</span>
            <RiskBadge risk={e.risk} />
            <span className="ml-auto font-mono text-[11px] text-zinc-500">{clock(e.at)}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[11px] text-zinc-500">
            <span>by {e.actor.label ?? e.actor.id}</span>
            <span>{e.planId}</span>
            {e.revisionAfter && <span>{e.revisionBefore || 'unknown'} → {e.revisionAfter}</span>}
            {e.confirmation && <span className="flex flex-wrap gap-1">{e.confirmation.scope.map((s) => <Chip key={s}>{s}</Chip>)}</span>}
          </div>
        </li>
      ))}
    </ol>
  )
}
