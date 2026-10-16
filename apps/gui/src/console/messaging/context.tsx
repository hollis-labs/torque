import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { SeamsContext } from '../events/context'
import { createActionBus, MockIdentityConfirm, type ActionBus } from '../seams'
import { registerMessagingActions } from './actions'
import { createNudgeLimiter, type NudgeLimiter } from './guards'
import { createMockMessagingClient } from './mock'
import type { MessagingClient } from './types'

export interface MessagingContextValue {
  client: MessagingClient
  bus: ActionBus
  operator?: string
}

const defaultClient = createMockMessagingClient()
const defaultBus = createActionBus({ identity: new MockIdentityConfirm({ mode: 'auto-grant' }) })
const MessagingContext = createContext<MessagingContextValue>({ client: defaultClient, bus: defaultBus })

/**
 * Provides the client and registers the messaging actions on the ActionBus.
 * Uses the ConsoleProvider's bus when mounted inside one, else `actionBus`
 * (tests/previews), else a local bus. Swap `client` for the live adapter.
 */
export function MessagingProvider({ client, actionBus, limiter, operator, children }: { client: MessagingClient; actionBus?: ActionBus; limiter?: NudgeLimiter; operator?: string; children: ReactNode }) {
  const seams = useContext(SeamsContext)
  const bus = useMemo(
    () => actionBus ?? seams?.actionBus ?? createActionBus({ identity: new MockIdentityConfirm({ mode: 'auto-grant' }) }),
    [actionBus, seams],
  )
  const lim = useMemo(() => limiter ?? createNudgeLimiter(), [limiter])
  useEffect(() => registerMessagingActions(bus, client, lim), [bus, client, lim])
  const value = useMemo(() => ({ client, bus, operator }), [client, bus, operator])
  return <MessagingContext.Provider value={value}>{children}</MessagingContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useMessaging = () => useContext(MessagingContext)
