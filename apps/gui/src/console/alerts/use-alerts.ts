import { useContext, useEffect, useMemo, useState } from 'react'
import { useConsoleEvents } from '../events/hooks'
import { ItemStateContext } from '../events/context'
import { effectiveStatus, type ItemStatus } from '../seams/state-store'
import { SEVERITY_ORDER } from '../events/types'
import { defaultAlertMapper } from './mapping'
import type { Alert, AlertMapper } from './types'

export interface AlertEntry {
  alert: Alert
  status: ItemStatus
  snoozeUntil?: number
  resolution?: string
}

export const isActiveStatus = (s: ItemStatus) => s === 'unread' || s === 'read'

/** Re-render on an interval so ages and snooze expiry stay current without a stream. */
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

/**
 * Alerts derived from the shared Console feed. State keys are EVENT ids; a
 * thread-level state (F1 keys handled-ness by threadId ?? id) also applies, so
 * dismissing a whole thread elsewhere clears its alerts here.
 */
export function useAlertEntries(
  mapper: AlertMapper = defaultAlertMapper,
  now: number,
  /** Optional pass over the mapped entries (A3: coalescing, escalation). Must be referentially stable. */
  post?: (entries: AlertEntry[], now: number) => AlertEntry[],
): AlertEntry[] {
  const events = useConsoleEvents({ hideHandled: false, newestFirst: true })
  const items = useContext(ItemStateContext)
  return useMemo(() => {
    const out: AlertEntry[] = []
    for (const e of events) {
      const alert = mapper(e)
      if (!alert) continue
      const st = items[e.id] ?? (e.threadId ? items[e.threadId] : undefined)
      out.push({ alert, status: effectiveStatus(st, now), snoozeUntil: st?.snoozeUntil, resolution: st?.resolution })
    }
    return post ? post(out, now) : out
  }, [events, items, mapper, now, post])
}

export function summarize(entries: AlertEntry[]) {
  const active = entries.filter((e) => isActiveStatus(e.status))
  const history = entries.filter((e) => !isActiveStatus(e.status))
  const unreadEntries = active.filter((e) => e.status === 'unread')
  let top: Alert['severity'] | null = null
  for (const e of unreadEntries) if (top === null || SEVERITY_ORDER.indexOf(e.alert.severity) > SEVERITY_ORDER.indexOf(top)) top = e.alert.severity
  return { active, history, unread: unreadEntries.length, topUnread: top }
}
