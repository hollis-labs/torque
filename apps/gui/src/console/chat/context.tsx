import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { createMockChatClient } from './mock'
import type { ChatClient } from './types'

const defaultClient = createMockChatClient()
const ChatContext = createContext<{ client: ChatClient; operator?: string }>({ client: defaultClient })

/** Provides the ChatClient. Swap the mock for the mail or session-stream client here. */
export function ChatProvider({ client, operator, children }: { client: ChatClient; operator?: string; children: ReactNode }) {
  const value = useMemo(() => ({ client, operator }), [client, operator])
  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useChat = () => useContext(ChatContext)
