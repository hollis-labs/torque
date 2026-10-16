import { createContext, useContext } from 'react'
import type { ToastQueue } from './toast-queue'
import type { AlertEntry } from './use-alerts'
import type { AlertPrefs } from './types'

export interface AlertsController {
  entries: AlertEntry[]
  active: AlertEntry[]
  history: AlertEntry[]
  unread: number
  /** Highest severity among unread alerts, or null. */
  topUnread: AlertEntry['alert']['severity'] | null
  now: number
  open: boolean
  setOpen: (open: boolean) => void
  prefs: AlertPrefs
  setPrefs: (patch: Partial<AlertPrefs>) => void
  queue: ToastQueue
  markRead: (id: string) => void
  markAllRead: () => void
  dismiss: (id: string) => void
  snooze: (id: string, ms: number) => void
  restore: (id: string) => void
  /** Alert open in the detail modal (A2), or null. */
  expandedId: string | null
  expand: (id: string | null) => void
}

export const AlertsContext = createContext<AlertsController | null>(null)

export function useAlertsController(): AlertsController {
  const c = useContext(AlertsContext)
  if (!c) throw new Error('useAlertsController must be used inside <AlertHost>')
  return c
}

/** Null outside a host, so triggers can render nothing when alerts are not enabled. */
export function useOptionalAlertsController(): AlertsController | null {
  return useContext(AlertsContext)
}
