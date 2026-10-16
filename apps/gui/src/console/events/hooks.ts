import { useContext, useMemo, useSyncExternalStore } from 'react'
import { effectiveStatus, type ItemState } from '../seams/state-store'
import { ItemStateContext, SeamsContext, StoreContext } from './context'
import type { ConsoleSeams } from './provider'
import type { ConsoleState } from './reducer'
import type { ConsoleStore } from './store'
import type { ConsoleEvent } from './types'

export function useConsoleStore(): ConsoleStore {
  const s = useContext(StoreContext)
  if (!s) throw new Error('useConsoleStore must be used inside <ConsoleProvider>')
  return s
}

export function useConsoleSeams(): ConsoleSeams {
  const s = useContext(SeamsContext)
  if (!s) throw new Error('useConsoleSeams must be used inside <ConsoleProvider>')
  return s
}

/** Whole store state. Prefer the narrower hooks below. */
export function useConsoleState(): ConsoleState {
  const store = useConsoleStore()
  return useSyncExternalStore(store.subscribe, store.getState, store.getState)
}

export interface ConsoleEventFilter {
  kinds?: string[]
  /** Kind prefix, e.g. "pr." */
  kindPrefix?: string
  minSeverity?: ConsoleEvent['severity']
  subject?: { type: string; id?: string }
  threadId?: string
  /** Hide dismissed/settled/snoozed items (default true). */
  hideHandled?: boolean
  /** Newest first (default true). */
  newestFirst?: boolean
  limit?: number
}

const SEV = ['debug', 'info', 'notice', 'warning', 'error', 'critical']

/** Filtered view over the shared event list; no stream is opened. */
export function useConsoleEvents(filter: ConsoleEventFilter = {}): ConsoleEvent[] {
  const { events } = useConsoleState()
  const items = useContext(ItemStateContext)
  const { kinds, kindPrefix, minSeverity, subject, threadId, hideHandled = true, newestFirst = true, limit } = filter
  const kindsKey = kinds?.join('|')
  return useMemo(() => {
    // eslint-disable-next-line react-hooks/purity -- snooze expiry is evaluated against wall time on each recompute
    const now = Date.now()
    const min = minSeverity ? SEV.indexOf(minSeverity) : 0
    let out = events.filter((e) => {
      if (kinds && !kinds.includes(e.kind)) return false
      if (kindPrefix && !e.kind.startsWith(kindPrefix)) return false
      if (SEV.indexOf(e.severity) < min) return false
      if (subject && !e.subjects.some((s) => s.type === subject.type && (subject.id === undefined || s.id === subject.id))) return false
      if (threadId && e.threadId !== threadId) return false
      if (hideHandled) {
        const st = effectiveStatus(items[e.threadId ?? e.id] ?? items[e.id], now)
        if (st === 'dismissed' || st === 'settled' || st === 'snoozed') return false
      }
      return true
    })
    if (newestFirst) out = [...out].reverse()
    return limit ? out.slice(0, limit) : out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [events, items, kindsKey, kindPrefix, minSeverity, subject?.type, subject?.id, threadId, hideHandled, newestFirst, limit])
}

/** Server-backed state of one item (event id or thread id), with mutators routed through the StateStore. */
export function useItemState(key: string) {
  const { stateStore } = useConsoleSeams()
  const items = useContext(ItemStateContext)
  return {
    state: items[key] as ItemState | undefined,
    status: effectiveStatus(items[key]),
    read: () => stateStore.apply({ type: 'read', key }),
    dismiss: (note?: string) => stateStore.apply({ type: 'dismiss', key, note }),
    snooze: (until: number, note?: string) => stateStore.apply({ type: 'snooze', key, until, note }),
    settle: (resolution?: string, note?: string) => stateStore.apply({ type: 'settle', key, resolution, note }),
    reopen: () => stateStore.apply({ type: 'reopen', key }),
  }
}
