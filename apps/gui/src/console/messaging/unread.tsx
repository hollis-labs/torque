import { useMemo, type ReactNode } from 'react'
import { useMessaging } from './context'
import { UnreadContext } from './unread-hook'
import { createUnreadStore } from './unread-store'

/** Mount once in the Console shell, under MessagingProvider. */
export function AgentUnreadProvider({ children }: { children: ReactNode }) {
  const { client } = useMessaging()
  const store = useMemo(() => createUnreadStore(client), [client])
  return <UnreadContext.Provider value={store}>{children}</UnreadContext.Provider>
}
