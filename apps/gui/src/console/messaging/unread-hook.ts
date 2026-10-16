import { createContext, useContext, useEffect, useSyncExternalStore } from 'react'
import type { UnreadStore } from './unread-store'

export const UnreadContext = createContext<UnreadStore | null>(null)
const noopSubscribe = () => () => undefined

/** Unread count for one address from the shared batched poll; null when unknown, disabled or `address` is null. */
export function useAgentUnread(address: string | null): number | null {
  const store = useContext(UnreadContext)
  useEffect(() => (store && address ? store.watch(address) : undefined), [store, address])
  return useSyncExternalStore(store?.subscribe ?? noopSubscribe, () => (store && address ? store.get(address) : null))
}
