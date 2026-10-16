import { useEffect, useState } from 'react'
import { ago } from '@/lib/agent-activity'
import type { Recipient } from '../messaging'
import { CapabilityBadge } from './CapabilityBadge'
import { ChatComposer } from './ChatComposer'
import { useChat } from './context'
import { useChatThread, useChatThreads } from './hooks'
import { ThreadView } from './ThreadView'
import type { ChatThread } from './types'

function ThreadRow({ t, now, onOpen }: { t: ChatThread; now: number; onOpen: () => void }) {
  return (
    <li className="border-b border-zinc-800/80">
      <button type="button" onClick={onOpen} className="block w-full px-3 py-2 text-left hover:bg-zinc-900/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-amber-300">
        <div className="flex items-center justify-between gap-3">
          <span className="truncate font-mono text-xs text-zinc-100">{t.title}</span>
          {t.unread > 0 && <span aria-label={`${t.unread} unread`} className="border border-amber-400/60 px-1.5 font-mono text-[10px] text-amber-300">{t.unread}</span>}
        </div>
        <div className="mt-1 flex items-center justify-between gap-3">
          <CapabilityBadge capability={t.capability} />
          {t.lastAt > 0 && <span className="font-mono text-[10px] text-zinc-500">{ago(t.lastAt, now)}</span>}
        </div>
        {t.lastPreview && <p className="mt-1 truncate font-mono text-[11px] text-zinc-500">{t.lastPreview}</p>}
      </button>
    </li>
  )
}

function Conversation({ thread, onBack }: { thread: ChatThread; onBack?: () => void }) {
  const { messages, capability, loading, hasMore, loadOlder, send, sendError } = useChatThread(thread.id)
  const cap = capability ?? thread.capability
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="space-y-1 border-b border-zinc-800 px-3 py-2">
        <div className="flex items-center gap-3">
          {onBack && <button type="button" onClick={onBack} aria-label="Back to threads" className="font-mono text-xs text-zinc-400 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">&lt; Threads</button>}
          <h2 className="truncate font-mono text-sm text-zinc-100">{thread.title}</h2>
        </div>
        <CapabilityBadge capability={cap} withHint />
      </header>
      <ThreadView messages={messages} loading={loading} hasMore={hasMore} onLoadOlder={() => void loadOlder()} />
      <ChatComposer onSend={send} capability={cap} error={sendError} />
    </div>
  )
}

export interface ChatPanelProps {
  /** Jump straight into the conversation with this agent (from an agent row or alert). */
  agent?: Recipient | null
  className?: string
}

/** Thread list plus conversation. Fits a drawer; stateless apart from the open thread. */
export function ChatPanel({ agent, className }: ChatPanelProps) {
  const { client } = useChat()
  const threads = useChatThreads()
  const [now] = useState(() => Date.now())
  const [openId, setOpenId] = useState<string | null>(null)
  const [opened, setOpened] = useState<ChatThread | null>(null)
  const addr = agent?.address
  useEffect(() => {
    if (!agent) return
    let live = true
    client.openThread(agent).then((t) => {
      if (live) {
        setOpened(t)
        setOpenId(t.id)
      }
    }, () => undefined)
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, addr])

  const active = openId ? (threads?.find((t) => t.id === openId) ?? (opened?.id === openId ? opened : null)) : null
  return (
    <section aria-label="Chat" className={`flex min-h-0 flex-col bg-zinc-950 ${className ?? ''}`}>
      {active ? (
        <Conversation key={active.id} thread={active} onBack={() => setOpenId(null)} />
      ) : (
        <>
          <header className="border-b border-zinc-800 px-3 py-2">
            <h2 className="font-mono text-sm text-zinc-100">Chat</h2>
          </header>
          {threads === null ? (
            <p className="px-3 py-4 font-mono text-xs text-zinc-500">Loading</p>
          ) : threads.length === 0 ? (
            <p className="px-3 py-4 font-mono text-xs text-zinc-500">No conversations. Open one from an agent.</p>
          ) : (
            <ul aria-label="Threads" className="overflow-y-auto">{threads.map((t) => <ThreadRow key={t.id} t={t} now={now} onOpen={() => setOpenId(t.id)} />)}</ul>
          )}
        </>
      )}
    </section>
  )
}
