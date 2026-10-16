import { useEffect, useRef, useState, type ReactNode } from 'react'
import { MessageComposer, type MessageComposerProps } from './MessageComposer'
import type { MessageIntent, Recipient } from './types'

const LABEL: Record<MessageIntent, string> = { message: 'Message', nudge: 'Nudge', update_request: 'Request update' }
const trigger = 'border border-zinc-700 px-2.5 py-1 font-mono text-[10px] uppercase tracking-widest text-zinc-300 transition-colors hover:border-zinc-500 hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-amber-300 disabled:cursor-not-allowed disabled:opacity-40'

function useDismiss(open: boolean, close: () => void, ref: React.RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!open) return
    const key = (e: KeyboardEvent) => e.key === 'Escape' && close()
    const down = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && close()
    document.addEventListener('keydown', key)
    document.addEventListener('mousedown', down)
    return () => {
      document.removeEventListener('keydown', key)
      document.removeEventListener('mousedown', down)
    }
  }, [open, close, ref])
}

/** Popover anchored under its trigger. */
function Popover({ label, disabled, title, children }: { label: string; disabled?: boolean; title?: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const close = () => setOpen(false)
  useDismiss(open, close, ref)
  return (
    <span ref={ref} className="relative inline-block">
      <button type="button" disabled={disabled} title={title} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen((o) => !o)} className={trigger}>{label}</button>
      {open && (
        <div role="dialog" aria-label={label} className="absolute left-0 top-full z-40 mt-1 w-[22rem] max-w-[90vw] border border-zinc-700 bg-zinc-950 p-3 shadow-xl">
          {children(close)}
        </div>
      )}
    </span>
  )
}

/** Centered dialog for the full composer. */
function Dialog({ label, disabled, title, children }: { label: string; disabled?: boolean; title?: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const close = () => setOpen(false)
  useEffect(() => {
    if (!open) return
    const key = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [open])
  return (
    <>
      <button type="button" disabled={disabled} title={title} aria-haspopup="dialog" onClick={() => setOpen(true)} className={trigger}>{label}</button>
      {open && (
        <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/60 p-4 pt-[10vh]" onMouseDown={(e) => e.target === e.currentTarget && close()}>
          <div ref={ref} role="dialog" aria-modal="true" aria-label={label} className="max-h-[80vh] w-full max-w-lg overflow-y-auto border border-zinc-700 bg-zinc-950 p-4 shadow-xl">
            {children(close)}
          </div>
        </div>
      )}
    </>
  )
}

type Common = Omit<MessageComposerProps, 'intent' | 'recipient' | 'onCancel'> & { recipient: Recipient | null }
const noRecipient = (r: Recipient | null) => (r ? undefined : 'No resolvable recipient')

export function SendMessageButton(p: Common) {
  return <Dialog label={LABEL.message} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="message" onCancel={close} />}</Dialog>
}
export function NudgeButton(p: Common) {
  return <Popover label={LABEL.nudge} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="nudge" showThread={false} onCancel={close} />}</Popover>
}
export function RequestUpdateButton(p: Common) {
  return <Popover label={LABEL.update_request} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="update_request" showThread={false} onCancel={close} />}</Popover>
}

/** The three actions as one row; mount it in the agent modal, alert cards and agent refs. */
export function AgentMessageActions(p: Common) {
  return (
    <div role="group" aria-label="Message actions" className="flex flex-wrap items-center gap-2">
      <SendMessageButton {...p} />
      <NudgeButton {...p} />
      <RequestUpdateButton {...p} />
    </div>
  )
}
