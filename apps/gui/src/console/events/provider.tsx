import { useEffect, useState, type ReactNode } from 'react'
import { useApi } from '@/hooks/use-api'
import { createActionBus, type ActionBus } from '../seams/action-bus'
import { MockIdentityConfirm, type IdentityConfirm } from '../seams/identity-confirm'
import { MemoryStateStore, type ItemState, type StateStore } from '../seams/state-store'
import { ActivityStreamSource, CompositeSource, TorqueEventsSource } from './adapters'
import { FixtureSource } from './fixture-source'
import { resolveConsoleMode, type ConsoleMode } from './mode'
import { ItemStateContext, SeamsContext, StoreContext } from './context'
import type { ConsoleSource } from './source'
import { createConsoleStore, type ConsoleStore } from './store'

export interface ConsoleSeams {
  stateStore: StateStore
  actionBus: ActionBus
  identity: IdentityConfirm
}

export interface ConsoleProviderProps {
  children: ReactNode
  /** Overrides everything below (tests, storybook-style previews). */
  source?: ConsoleSource
  mode?: ConsoleMode
  stateStore?: StateStore
  identity?: IdentityConfirm
  actionBus?: ActionBus
  resyncMs?: number
}

/**
 * Mount ONCE at the app shell (inside the ApiProvider, above the router
 * outlet). It owns the only source subscription: routes consume it through
 * hooks and never open a stream of their own. Not mounted in App.tsx yet.
 *
 * Seams default to in-memory mocks; swap by prop when real backends exist.
 * The `source` and seam props are read once at mount (like useState's initial
 * value): changing them later does not rebuild the store.
 */
export function ConsoleProvider(props: ConsoleProviderProps) {
  const api = useApi()
  const [{ store, seams }] = useState<{ store: ConsoleStore; seams: ConsoleSeams }>(() => {
    const mode =
      props.mode ?? resolveConsoleMode({ search: typeof window !== 'undefined' ? window.location.search : '', env: import.meta.env.VITE_CONSOLE_SOURCE as string | undefined })
    const source =
      props.source ?? (mode === 'fixture' ? new FixtureSource() : new CompositeSource([new ActivityStreamSource(api), new TorqueEventsSource(api)]))
    const identity = props.identity ?? new MockIdentityConfirm()
    return {
      store: createConsoleStore({ source, resyncMs: props.resyncMs }),
      seams: {
        identity,
        stateStore: props.stateStore ?? new MemoryStateStore(),
        actionBus: props.actionBus ?? createActionBus({ identity }),
      },
    }
  })

  useEffect(() => {
    store.start()
    return () => store.stop()
  }, [store])

  // Item state mirror: loaded once, then kept current by the StateStore's push channel.
  const [items, setItems] = useState<Record<string, ItemState>>({})
  useEffect(() => {
    let active = true
    const merge = (rows: ItemState[]) => setItems((prev) => ({ ...prev, ...Object.fromEntries(rows.map((r) => [r.key, r])) }))
    const off = seams.stateStore.subscribe((rows) => active && merge(rows))
    seams.stateStore.load().then((rows) => active && merge(rows), () => undefined)
    return () => {
      active = false
      off()
    }
  }, [seams.stateStore])

  return (
    <StoreContext.Provider value={store}>
      <SeamsContext.Provider value={seams}>
        <ItemStateContext.Provider value={items}>{props.children}</ItemStateContext.Provider>
      </SeamsContext.Provider>
    </StoreContext.Provider>
  )
}

