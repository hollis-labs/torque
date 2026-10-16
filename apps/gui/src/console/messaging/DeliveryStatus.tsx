import { useEffect, useState } from 'react'
import { useMessaging } from './context'
import { DELIVERY_ORDER, type DeliveryState } from './types'

const STYLE: Record<DeliveryState, { label: string; dot: string; text: string }> = {
  queued: { label: 'QUEUED', dot: 'bg-zinc-500', text: 'text-zinc-400' },
  delivered: { label: 'DELIVERED', dot: 'bg-sky-400', text: 'text-sky-300' },
  read: { label: 'READ', dot: 'bg-emerald-400', text: 'text-emerald-300' },
  replied: { label: 'REPLIED', dot: 'bg-emerald-400', text: 'text-emerald-300' },
  failed: { label: 'FAILED', dot: 'bg-red-400', text: 'text-red-300' },
}

/** Badge for a known state; `steps` adds the queued > delivered > read > replied rail. */
export function DeliveryBadge({ state, steps }: { state: DeliveryState; steps?: boolean }) {
  const s = STYLE[state]
  const reached = DELIVERY_ORDER.indexOf(state)
  return (
    <span className={`inline-flex items-center gap-2 font-mono text-[10px] tracking-widest ${s.text}`} data-state={state}>
      {steps && state !== 'failed' && (
        <span className="inline-flex gap-0.5" aria-hidden>
          {DELIVERY_ORDER.map((d, i) => (
            <span key={d} className={`h-1.5 w-3 ${i <= reached ? s.dot : 'bg-zinc-800'}`} />
          ))}
        </span>
      )}
      {!steps && <span className={`h-1.5 w-1.5 ${s.dot}`} aria-hidden />}
      {s.label}
    </span>
  )
}

const TERMINAL: DeliveryState[] = ['replied', 'failed']

/** Polls the client for a sent message until it is replied/failed. */
export function DeliveryStatus({ messageId, initial = 'queued', pollMs = 1500 }: { messageId: string; initial?: DeliveryState; pollMs?: number }) {
  const { client } = useMessaging()
  const [state, setState] = useState<DeliveryState>(initial)
  useEffect(() => {
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      let next: DeliveryState | null = null
      try {
        next = await client.getDelivery(messageId)
      } catch {
        /* keep last known state and retry */
      }
      if (!live) return
      if (next) setState(next)
      if (!next || !TERMINAL.includes(next)) timer = setTimeout(tick, pollMs)
    }
    void tick()
    return () => {
      live = false
      if (timer) clearTimeout(timer)
    }
  }, [client, messageId, pollMs])
  return <DeliveryBadge state={state} steps />
}
