import { useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from 'react'
import type { StateOp } from '../seams/state-store'
import { useConsoleSeams, useConsoleState } from '../events/hooks'
import { AlertsContext, type AlertsController } from './context'
import { AlertToastStack } from './AlertToastStack'
import { AlertTray } from './AlertTray'
import { AlertDetailModal } from './detail/AlertDetailModal'
import { createMockDecisionClient, registerAlertDecisionActions, type AlertActionResolver, type AlertDecisionClient } from './detail/actions'
import { defaultAlertMapper } from './mapping'
import { applyEscalation, coalesceEntries } from '../rules/coalesce'
import { createRuleMapper } from '../rules/engine'
import type { AlertRulePack } from '../rules/types'
import { LocalAlertPrefsStore, type AlertPrefsStore } from './prefs'
import { ToastQueue } from './toast-queue'
import { summarize, useAlertEntries, useNow } from './use-alerts'
import type { AlertEntry } from './use-alerts'
import type { AlertMapper } from './types'

export interface AlertHostProps {
  children?: ReactNode
  /** Swap the event -> alert mapping (A3 rules, K1). */
  mapper?: AlertMapper
  /** Declarative alert rules (A3). Ignored when `mapper` is given; without either, A1's default mapper is used. */
  rules?: AlertRulePack
  /** Defaults to a localStorage-backed store. */
  prefsStore?: AlertPrefsStore
  /** Approve / deny backend (A2). Defaults to a mock; swap for the live client. */
  decisionClient?: AlertDecisionClient
  /** Override the per-kind action set shown in the detail modal. */
  actions?: AlertActionResolver
}

/**
 * Global alert surface. Reads the shared Console feed (no stream of its own),
 * raises toasts for new alerts, and exposes the tray via context. Render the
 * trigger anywhere beneath it (<AlertTrayTrigger />). Must sit inside <ConsoleProvider>.
 */
export function AlertHost({ children, mapper: mapperProp, rules, prefsStore, decisionClient, actions }: AlertHostProps) {
  const mapper = useMemo(() => mapperProp ?? (rules ? createRuleMapper(rules) : defaultAlertMapper), [mapperProp, rules])
  const { stateStore, actionBus } = useConsoleSeams()
  const [mockDecisions] = useState(() => createMockDecisionClient())
  const decisions = decisionClient ?? mockDecisions
  useEffect(() => registerAlertDecisionActions(actionBus, decisions), [actionBus, decisions])
  const { ready } = useConsoleState()
  const [store] = useState<AlertPrefsStore>(() => prefsStore ?? new LocalAlertPrefsStore())
  const [queue] = useState(() => new ToastQueue())
  const [open, setOpen] = useState(false)
  const [expandedId, expand] = useState<string | null>(null)
  const prefs = useSyncExternalStore(store.subscribe, store.get, store.get)
  const now = useNow()
  const windowMs = prefs.coalesceWindowSec * 1000
  const post = useCallback((e: AlertEntry[], t: number) => applyEscalation(coalesceEntries(e, windowMs), t), [windowMs])
  const entries = useAlertEntries(mapper, now, post)
  const summary = useMemo(() => summarize(entries), [entries])

  // New unread alerts -> toasts. The queue ignores the backlog present at first offer.
  const unreadAlerts = useMemo(() => summary.active.filter((e) => e.status === 'unread').map((e) => e.alert), [summary.active])
  useEffect(() => {
    // Wait for the first snapshot so the backlog is not mistaken for new events.
    if (ready) queue.offer(unreadAlerts, prefs, Date.now())
  }, [queue, unreadAlerts, prefs, ready])

  const apply = useCallback(
    (op: StateOp) => {
      void stateStore.apply(op).catch(() => undefined)
    },
    [stateStore],
  )

  const controller = useMemo<AlertsController>(
    () => ({
      ...summary,
      entries,
      now,
      open,
      setOpen,
      prefs,
      setPrefs: (p) => store.set(p),
      queue,
      markRead: (id) => apply({ type: 'read', key: id }),
      markAllRead: () => summary.active.filter((e) => e.status === 'unread').forEach((e) => apply({ type: 'read', key: e.alert.id })),
      dismiss: (id) => apply({ type: 'dismiss', key: id }),
      snooze: (id, ms) => apply({ type: 'snooze', key: id, until: Date.now() + ms }),
      restore: (id) => apply({ type: 'reopen', key: id }),
      expandedId,
      expand,
    }),
    [summary, entries, now, open, prefs, store, queue, apply, expandedId],
  )

  return (
    <AlertsContext.Provider value={controller}>
      {children}
      <AlertToastStack />
      <AlertTray />
      <AlertDetailModal mapper={mapper} actions={actions} />
    </AlertsContext.Provider>
  )
}
