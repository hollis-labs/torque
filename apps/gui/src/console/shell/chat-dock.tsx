import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { MessageSquare } from 'lucide-react'
import type { Recipient } from '../messaging'
import { ChatDock } from '../chat/ChatDock'
import { useChatUnread } from '../chat/hooks'

interface ChatDockApi {
  open: boolean
  /** Open the dock, optionally straight into one agent's conversation. */
  openChat: (agent?: Recipient | null) => void
  closeChat: () => void
}

const ChatDockContext = createContext<ChatDockApi | null>(null)

/** Owns the dock's open state and renders the drawer. Mount inside ChatProvider. */
export function ChatDockProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ open: boolean; agent: Recipient | null }>({ open: false, agent: null })
  const openChat = useCallback((agent?: Recipient | null) => setState({ open: true, agent: agent ?? null }), [])
  const closeChat = useCallback(() => setState((s) => ({ ...s, open: false })), [])
  const api = useMemo<ChatDockApi>(() => ({ open: state.open, openChat, closeChat }), [state.open, openChat, closeChat])
  return (
    <ChatDockContext.Provider value={api}>
      {children}
      <ChatDock open={state.open} onClose={closeChat} agent={state.agent} />
    </ChatDockContext.Provider>
  )
}

/** Null outside an enabled Console, so callers can render nothing. */
// eslint-disable-next-line react-refresh/only-export-components
export function useOptionalChatDock(): ChatDockApi | null {
  return useContext(ChatDockContext)
}

/** Nav-rail trigger (h-9 w-9 like the bell). Renders nothing when the Console is off. */
export function ChatDockTrigger() {
  const dock = useOptionalChatDock()
  if (!dock) return null
  return <ChatTrigger dock={dock} />
}

function ChatTrigger({ dock }: { dock: ChatDockApi }) {
  const unread = useChatUnread()
  return (
    <button
      type="button"
      data-chat-trigger=""
      aria-label={unread > 0 ? `Chat, ${unread} unread` : 'Chat'}
      aria-haspopup="dialog"
      aria-expanded={dock.open}
      onClick={() => (dock.open ? dock.closeChat() : dock.openChat())}
      className={`relative flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 ${dock.open ? 'bg-accent text-foreground' : ''}`}
    >
      <MessageSquare aria-hidden="true" className="h-4 w-4" />
      {unread > 0 && <span aria-hidden="true" className="absolute -right-0.5 -top-0.5 min-w-[14px] border border-amber-400/60 bg-zinc-950 px-1 text-center font-mono text-[9px] leading-[12px] text-amber-300">{unread}</span>}
    </button>
  )
}

/** Bell + chat for the nav rail footer. */
export function ConsoleNavExtras({ alerts }: { alerts: ReactNode }) {
  return (
    <>
      {alerts}
      <ChatDockTrigger />
    </>
  )
}
