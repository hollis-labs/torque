import { SEVERITY_ORDER } from '../events/types'
import type { AlertEntry } from '../alerts/use-alerts'

/**
 * Fold bursts: entries (newest first) that share a coalesceKey and status, and
 * are each within the window of the previous member, collapse into the newest.
 * windowMs <= 0 (the user turned coalescing off) disables folding entirely; a rule's
 * own window otherwise overrides the user's.
 */
export function coalesceEntries(entries: AlertEntry[], windowMs: number): AlertEntry[] {
  if (windowMs <= 0) return entries
  const open = new Map<string, { rep: number; lastTs: number; count: number }>()
  const out: AlertEntry[] = []
  for (const entry of entries) {
    const { alert } = entry
    if (!alert.coalesceKey) {
      out.push(entry)
      continue
    }
    const gk = `${entry.status === 'unread' || entry.status === 'read' ? 'active' : entry.status}\u0000${alert.coalesceKey}`
    const win = alert.coalesceWindowMs ?? windowMs
    const g = open.get(gk)
    if (g && g.lastTs - alert.ts <= win) {
      g.lastTs = alert.ts
      g.count += 1
      const rep = out[g.rep]
      const base = rep.alert.title.replace(/ \(×\d+\)$/, '')
      out[g.rep] = { ...rep, alert: { ...rep.alert, count: g.count, title: `${base} (×${g.count})` } }
      continue
    }
    open.set(gk, { rep: out.length, lastTs: alert.ts, count: 1 })
    out.push(entry)
  }
  return out
}

/** Unread alerts older than their rule's escalation delay are shown at the escalated severity. */
export function applyEscalation(entries: AlertEntry[], now: number): AlertEntry[] {
  let changed = false
  const out = entries.map((entry) => {
    const { alert } = entry
    if (entry.status !== 'unread' || !alert.escalate) return entry
    if (now - alert.ts < alert.escalate.afterMs) return entry
    if (SEVERITY_ORDER.indexOf(alert.escalate.to) <= SEVERITY_ORDER.indexOf(alert.severity)) return entry
    changed = true
    return { ...entry, alert: { ...alert, severity: alert.escalate.to } }
  })
  return changed ? out : entries
}
