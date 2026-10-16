// Card runtime: registry + rule set + host-surface state, provided once under
// ConsoleProvider. Hooks here turn the shared event list into live CardInstances.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { ItemStateContext } from '../events/context'
import { useConsoleSeams, useConsoleState } from '../events/hooks'
import { effectiveStatus } from '../seams/state-store'
import type { ConsoleAction } from '../seams/action-bus'
import { deriveCards } from './engine'
import type { CardRuleSet } from './rules'
import type { CardRegistry } from './registry'
import { type CardHost } from './host'
import type { CardActionController, CardActionSpec, CardInstance, CardSurface } from './types'

export interface CardRuntime {
  registry: CardRegistry
  rules: CardRuleSet
  host: CardHost
}

export const RuntimeContext = createContext<CardRuntime | null>(null)

export interface CardRuntimeProviderProps {
  children: ReactNode
  rules?: CardRuleSet
  registry?: CardRegistry
  host?: CardHost
  /** Mount the toast stack and modal (default true). A1/H1 may mount their own instead. */
  mountSurfaces?: boolean
}

export function useCardRuntime(): CardRuntime {
  const r = useContext(RuntimeContext)
  if (!r) throw new Error('Card hooks must be used inside <CardRuntimeProvider> (under <ConsoleProvider>)')
  return r
}

/** Host-surface API: toast / modal / tray / manual cards. Stable identity. */
export function useCardHost(): CardHost {
  return useCardRuntime().host
}

export function useCardRegistry(): CardRegistry {
  return useCardRuntime().registry
}

export interface UseCardsOptions {
  surface?: CardSurface
  kinds?: string[]
  /** Only cards about this subject. */
  subject?: { type: string; id?: string }
  /** Include dismissed / snoozed cards (default false). Settled and resolved cards show as phase "resolved". */
  includeHandled?: boolean
  /** Include resolved / expired / superseded cards (default false for tray-like lists). */
  includeClosed?: boolean
  limit?: number
}

const SEV_RANK = { debug: 0, info: 1, notice: 2, warning: 3, error: 4, critical: 5 } as const

/** Live cards derived from the shared event list + server-backed item state + manual cards. */
export function useCards(opts: UseCardsOptions = {}): CardInstance[] {
  const { rules, registry, host } = useCardRuntime()
  const { events } = useConsoleState()
  const items = useContext(ItemStateContext)
  const manual = useSyncExternalStore(host.subscribe, host.getManual, host.getManual)
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000)
    return () => clearInterval(t)
  }, [])
  const { surface, kinds, subject, includeHandled = false, includeClosed = false, limit } = opts
  const kindsKey = kinds?.join('|')
  return useMemo(() => {
    // eslint-disable-next-line react-hooks/purity -- expiry is evaluated against wall time; `tick` re-runs it
    const now = Date.now()
    const derived = [...deriveCards(events, rules, { now }), ...Object.values(manual)]
    const out: CardInstance[] = []
    for (const c0 of derived) {
      const st = items[c0.id]
      const status = effectiveStatus(st, now)
      if (!includeHandled && (status === 'dismissed' || status === 'snoozed')) continue
      let c = c0
      if (status === 'settled' && c.phase === 'live') c = { ...c, phase: 'resolved', resolution: { label: st?.resolution ? st.resolution[0].toUpperCase() + st.resolution.slice(1) : 'Handled', ts: st?.updatedAt ?? now } }
      if (!includeClosed && c.phase !== 'live') continue
      if (kinds && !kinds.includes(c.kind)) continue
      const allowed = registry.get(c.kind)?.surfaces
      if (surface && (!c.surfaces.includes(surface) || (allowed && !allowed.includes(surface)))) continue
      if (subject && !c.subjects.some((s) => s.type === subject.type && (subject.id === undefined || s.id === subject.id))) continue
      out.push(c)
    }
    out.sort(
      (a, b) =>
        SEV_RANK[b.severity] - SEV_RANK[a.severity] ||
        (registry.get(b.kind)?.priority ?? 0) - (registry.get(a.kind)?.priority ?? 0) ||
        b.ts - a.ts,
    )
    return limit ? out.slice(0, limit) : out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [events, rules, registry, manual, items, tick, surface, kindsKey, subject?.type, subject?.id, includeHandled, includeClosed, limit])
}

/** Resolve one card by id regardless of surface/handled state (for modals and deep links). */
export function useCard(id: string | null): CardInstance | undefined {
  const all = useCards({ includeHandled: true, includeClosed: true })
  return useMemo(() => (id ? all.find((c) => c.id === id) : undefined), [all, id])
}

/** Action controller for one card: dispatches through the ActionBus (IdentityConfirm included) and the StateStore. */
export function useCardController(card: CardInstance): CardActionController {
  const { actionBus, stateStore } = useConsoleSeams()
  const [status, setStatus] = useState<CardActionController['status']>({})
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const put = useCallback((id: string, v: CardActionController['status'][string]) => {
    if (alive.current) setStatus((s) => ({ ...s, [id]: v }))
  }, [])
  const dismiss = useCallback(async () => {
    await stateStore.apply({ type: 'dismiss', key: card.id })
  }, [stateStore, card.id])
  const snooze = useCallback(
    async (ms: number) => {
      await stateStore.apply({ type: 'snooze', key: card.id, until: Date.now() + ms })
    },
    [stateStore, card.id],
  )
  const run = useCallback(
    async (a: CardActionSpec) => {
      if (a.kind === 'dismiss') return dismiss()
      if (a.kind === 'snooze') return snooze(typeof a.params?.ms === 'number' ? a.params.ms : 3_600_000)
      put(a.id, { pending: true })
      const act: ConsoleAction = {
        kind: a.kind,
        actionId: a.id,
        eventId: card.event.id,
        subject: card.subjects[0],
        params: a.params,
        confirm: a.confirm,
      }
      const res = await actionBus.dispatch(act)
      const ok = res.status === 'ok'
      if (ok && a.settlesAs) await stateStore.apply({ type: 'settle', key: card.id, resolution: a.settlesAs })
      put(a.id, { pending: false, ok, message: res.message ?? (ok ? 'Done' : res.status) })
    },
    [actionBus, stateStore, card, dismiss, snooze, put],
  )
  return useMemo(() => ({ run, status, dismiss, snooze }), [run, status, dismiss, snooze])
}
