import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { useChat } from './context'
import { applyChatEvent, mergeMessages, type ChatThreadState } from './reducer'
import type { ChatEvent, ChatMessage, ChatThread } from './types'

const PAGE = 20

type Action = { type: 'event'; e: ChatEvent } | { type: 'history'; messages: ChatMessage[] }
const reduce = (s: ChatThreadState, a: Action): ChatThreadState =>
  a.type === 'event' ? applyChatEvent(s, a.e) : a.type === 'history' ? { ...s, messages: mergeMessages(s.messages, a.messages) } : s

export function useChatThreads() {
  const { client } = useChat()
  const [threads, setThreads] = useState<ChatThread[] | null>(null)
  useEffect(() => {
    let live = true
    client.listThreads().then((t) => live && setThreads(t), () => live && setThreads([]))
    const off = client.subscribeThreads((t) => live && setThreads(t))
    return () => {
      live = false
      off()
    }
  }, [client])
  return threads
}

/** Total unread across threads. Feed this to the alert tray / a badge. */
export function useChatUnread(): number {
  const threads = useChatThreads()
  return (threads ?? []).reduce((n, t) => n + t.unread, 0)
}

export function useChatThread(threadId: string | null) {
  const { client } = useChat()
  const [state, dispatch] = useReducer(reduce, { messages: [] })
  const [loading, setLoading] = useState(!!threadId)
  const [hasMore, setHasMore] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)
  const oldest = useRef<number | undefined>(undefined)

  useEffect(() => {
    // State starts fresh per thread: callers key the component on threadId.
    if (!threadId) return
    let live = true
    client.history(threadId, { limit: PAGE }).then(
      (p) => {
        if (!live) return
        dispatch({ type: 'history', messages: p.messages })
        setHasMore(p.hasMore)
        oldest.current = p.messages[0]?.at
        setLoading(false)
        void client.markRead(threadId)
      },
      () => live && setLoading(false),
    )
    const off = client.subscribe(threadId, (e) => {
      if (!live) return
      dispatch({ type: 'event', e })
      if (e.type === 'message' && e.message.from === 'agent') void client.markRead(threadId)
    })
    return () => {
      live = false
      off()
    }
  }, [client, threadId])

  const loadOlder = useCallback(async () => {
    if (!threadId || loading || !hasMore) return
    setLoading(true)
    try {
      const p = await client.history(threadId, { before: oldest.current, limit: PAGE })
      dispatch({ type: 'history', messages: p.messages })
      setHasMore(p.hasMore)
      oldest.current = p.messages[0]?.at ?? oldest.current
    } finally {
      setLoading(false)
    }
  }, [client, threadId, loading, hasMore])

  const send = useCallback(
    async (body: string) => {
      if (!threadId) return
      setSendError(null)
      try {
        const m = await client.send(threadId, body)
        dispatch({ type: 'event', e: { type: 'message', message: m } })
      } catch (err) {
        setSendError(err instanceof Error ? err.message : 'Could not send')
        throw err
      }
    },
    [client, threadId],
  )

  return { messages: state.messages, capability: state.capability, loading, hasMore, loadOlder, send, sendError }
}
