// event -> alert mapping. Isolated and swappable: pass a different AlertMapper
// to <AlertHost mapper={...}>. F1's kinds are placeholders, so unknown kinds
// degrade to a generic alert (notice and above) rather than being dropped.

import { KINDS, type ConsoleEvent } from '../events/types'
import { severityAtLeast, safeHref } from './policy'
import type { Alert, AlertIconKey, AlertLink, AlertMapper } from './types'

const str = (v: unknown): string | undefined => (typeof v === 'string' && v.trim() ? v.trim() : undefined)
const line = (v: string | undefined, max = 140): string | undefined => {
  if (!v) return undefined
  const one = v.replace(/\s+/g, ' ')
  return one.length > max ? `${one.slice(0, max - 1)}…` : one
}

export function sourceOf(e: ConsoleEvent): string {
  const agent = e.subjects.find((s) => s.type === 'agent')
  const who = agent?.label ?? agent?.id ?? e.source.id
  return e.source.machine ? `${who} · ${e.source.machine}` : who
}

export function linkOf(e: ConsoleEvent): AlertLink | undefined {
  const offered = e.actions.find((a) => a.kind === 'navigate' && typeof a.params?.href === 'string')
  if (offered) {
    const href = safeHref(offered.params?.href as string)
    if (href) return { label: offered.label, href }
  }
  const task = e.subjects.find((s) => s.type === 'task')
  if (task) return { label: 'Open task', href: `/tasks/${encodeURIComponent(task.id)}` }
  const subj = e.subjects.find((s) => safeHref(s.url))
  if (subj) return { label: 'Open', href: safeHref(subj.url) as string }
  if (e.subjects.some((s) => s.type === 'agent')) return { label: 'View agents', href: '/agents' }
  return undefined
}

function subjectLabel(e: ConsoleEvent): string {
  const s = e.subjects.find((x) => x.type !== 'agent') ?? e.subjects[0]
  return s?.label ?? s?.id ?? e.kind
}

export const defaultAlertMapper: AlertMapper = (e) => {
  const p = e.payload as Record<string, unknown>
  const base = { id: e.id, threadId: e.threadId, severity: e.severity, ts: e.ts, kind: e.kind, source: sourceOf(e), primary: linkOf(e) }
  const mk = (icon: AlertIconKey, title: string, detail?: string): Alert => ({ ...base, icon, title, detail: line(detail) })

  switch (e.kind) {
    case KINDS.metricTick:
      return null
    case KINDS.agentState: {
      if (p.state !== 'waiting') return null
      return mk('agent', `${subjectLabel(e)} is waiting`, str(p.waitingFor) ?? 'Waiting on you')
    }
    case KINDS.taskStateChanged: {
      const to = str(p.to) ?? 'updated'
      const from = str(p.from)
      return mk('task', `${subjectLabel(e)} → ${to}`, [str(p.title), from ? `was ${from}` : undefined].filter(Boolean).join(' · '))
    }
    case KINDS.messageReceived:
      return mk('message', str(p.subject) ?? `Message from ${str(p.from) ?? 'agent'}`, str(p.preview))
    case KINDS.approvalRequested:
      return mk('approval', str(p.title) ?? 'Approval requested', str(p.summary))
    case KINDS.approvalResolved:
      return mk('approval', str(p.title) ?? 'Approval resolved', str(p.summary))
    case KINDS.prOpened:
    case KINDS.prReviewRequested:
    case KINDS.prChecksFailed:
    case KINDS.prMerged: {
      const num = typeof p.number === 'number' ? `#${p.number}` : ''
      const verb = e.kind.slice(3).replace(/_/g, ' ')
      return mk('pr', `${str(p.repo) ?? 'PR'}${num} ${verb}`, str(p.title))
    }
    default:
      if (!severityAtLeast(e.severity, 'notice')) return null
      return mk(e.severity === 'error' || e.severity === 'critical' ? 'error' : e.severity === 'warning' ? 'warning' : 'info', e.kind, subjectLabel(e))
  }
}
