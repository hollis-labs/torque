import { useEffect } from 'react'
import type { Recipient } from '../messaging'
import { ChatPanel } from './ChatPanel'

/** Right-docked drawer hosting ChatPanel. The host owns `open`; Escape closes. */
export function ChatDock({ open, onClose, agent }: { open: boolean; onClose: () => void; agent?: Recipient | null }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
  if (!open) return null
  return (
    <aside aria-label="Chat drawer" className="fixed inset-y-0 right-0 z-40 flex w-full max-w-md flex-col border-l border-zinc-800 bg-zinc-950 shadow-xl">
      <div className="flex items-center justify-end border-b border-zinc-800 px-3 py-1">
        <button type="button" onClick={onClose} aria-label="Close chat" className="font-mono text-[11px] uppercase tracking-widest text-zinc-400 hover:text-zinc-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">Close</button>
      </div>
      <ChatPanel agent={agent} className="flex-1" />
    </aside>
  )
}

/** Compact trigger with unread badge, for agent rows and the console header. */
export function ChatButton({ onClick, unread = 0, label = 'Chat' }: { onClick: () => void; unread?: number; label?: string }) {
  return (
    <button type="button" onClick={onClick} className="inline-flex items-center gap-2 border border-zinc-700 px-2 py-1 font-mono text-[11px] uppercase tracking-widest text-zinc-300 hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300">
      {label}
      {unread > 0 && <span aria-label={`${unread} unread`} className="border border-amber-400/60 px-1 text-amber-300">{unread}</span>}
    </button>
  )
}
