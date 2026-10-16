import { useEffect, useRef, useState } from 'react'
import { ago } from '@/lib/agent-activity'
import { Linkified } from '../autolink'
import { DeliveryBadge } from '../messaging'
import { ToolChips } from './ToolChips'
import type { ChatMessage } from './types'

function Bubble({ m, now }: { m: ChatMessage; now: number }) {
  const mine = m.from === 'operator'
  return (
    <li data-from={m.from} data-streaming={m.streaming ? 'true' : undefined} className={`border-b border-zinc-800/80 px-3 py-2 ${mine ? 'bg-zinc-900/40' : ''}`}>
      <div className="flex items-center justify-between gap-3 font-mono text-[10px] uppercase tracking-widest text-zinc-500">
        <span>{mine ? 'you' : m.from === 'system' ? 'system' : 'agent'} · {ago(m.at, now)}</span>
        {mine && m.delivery && <DeliveryBadge state={m.delivery} />}
        {m.streaming && <span className="animate-pulse text-amber-300">STREAMING</span>}
      </div>
      {m.body && (
        <p className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-zinc-200">
          <Linkified text={m.body} preserveWhitespace />
          {m.streaming && <span aria-hidden className="ml-0.5 inline-block h-3 w-1.5 animate-pulse bg-amber-300 align-middle" />}
        </p>
      )}
      <ToolChips tools={m.tools} />
      {m.error && <p role="alert" className="mt-1 font-mono text-xs text-red-300">{m.error}</p>}
    </li>
  )
}

export function ThreadView({ messages, loading, hasMore, onLoadOlder }: { messages: ChatMessage[]; loading: boolean; hasMore: boolean; onLoadOlder: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)
  const prevHeight = useRef(0)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(t)
  }, [])
  useEffect(() => {
    const el = box.current
    if (!el) return
    if (prevHeight.current && !pinned.current && el.scrollTop === 0) el.scrollTop = el.scrollHeight - prevHeight.current // keep place after paging
    else if (pinned.current) el.scrollTop = el.scrollHeight
    prevHeight.current = 0
  }, [messages])

  return (
    <div
      ref={box}
      role="log"
      aria-label="Conversation"
      aria-live="polite"
      className="min-h-0 flex-1 overflow-y-auto"
      onScroll={(e) => {
        const el = e.currentTarget
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
      }}
    >
      {hasMore && (
        <div className="border-b border-zinc-800/80 px-3 py-2 text-center">
          <button
            type="button"
            disabled={loading}
            onClick={() => {
              prevHeight.current = box.current?.scrollHeight ?? 0
              pinned.current = false
              onLoadOlder()
            }}
            className="font-mono text-[10px] uppercase tracking-widest text-zinc-400 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:opacity-40"
          >
            {loading ? 'Loading' : 'Load older messages'}
          </button>
        </div>
      )}
      {messages.length === 0 ? (
        <p className="px-3 py-4 font-mono text-xs text-zinc-500">{loading ? 'Loading' : 'No messages yet. Say something.'}</p>
      ) : (
        <ul>{messages.map((m) => <Bubble key={m.id} m={m} now={now} />)}</ul>
      )}
    </div>
  )
}
