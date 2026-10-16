import { useId, useRef, useState } from 'react'
import { MAX_BODY } from '../messaging/guards'
import type { ChatCapability } from './types'

const btn = 'border px-3 py-1.5 font-mono text-[11px] uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40'

/** Compact chat input. Enter sends, Shift+Enter newline. Reuses M1's body cap. */
export function ChatComposer({ onSend, capability, error }: { onSend: (body: string) => Promise<void>; capability?: ChatCapability; error?: string | null }) {
  const id = useId()
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  const empty = !body.trim()
  const submit = async () => {
    if (empty || busy) return
    setBusy(true)
    try {
      await onSend(body.trim())
      setBody('')
    } catch {
      /* error shown via the `error` prop; keep the draft */
    } finally {
      setBusy(false)
      area.current?.focus()
    }
  }
  const note = capability?.mode === 'offline' ? 'Agent is offline. This will queue.' : capability?.mode === 'mail-only' ? 'Mail only. Reply may take a while.' : null
  return (
    <form
      aria-label="Send chat message"
      className="space-y-2 border-t border-zinc-800 p-3"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <label htmlFor={id} className="sr-only">Message</label>
      <textarea
        ref={area}
        id={id}
        rows={2}
        value={body}
        maxLength={MAX_BODY}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault()
            void submit()
          }
        }}
        placeholder="Message. Enter to send, Shift+Enter for a new line"
        className="w-full resize-none border border-zinc-700 bg-zinc-950 px-2 py-1.5 font-mono text-xs text-zinc-100 placeholder:text-zinc-600 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300"
      />
      {error && <p role="alert" className="font-mono text-xs text-red-300">{error}</p>}
      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[10px] text-zinc-500">{note}</span>
        <button type="submit" disabled={empty || busy} className={`${btn} border-amber-400/60 text-amber-300 hover:bg-amber-400/10`}>{busy ? 'Sending' : 'Send'}</button>
      </div>
    </form>
  )
}
