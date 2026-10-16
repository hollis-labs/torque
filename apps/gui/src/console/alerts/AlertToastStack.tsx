import { useSyncExternalStore } from 'react'
import { useAlertsController } from './context'
import { AlertToast } from './AlertToast'

/** Top-right stack (sonner owns bottom-right). Newest at the bottom of the stack. */
export function AlertToastStack() {
  const { queue, now, markRead, expand } = useAlertsController()
  const toasts = useSyncExternalStore(queue.subscribe, queue.getSnapshot, queue.getSnapshot)
  if (toasts.length === 0) return null
  return (
    <div role="region" aria-label="Alert notifications" className="pointer-events-none fixed right-3 top-3 z-50 md:[html[data-chat-open]_&]:right-[29rem] flex w-[min(22rem,calc(100vw-1.5rem))] flex-col gap-2">
      {toasts.map((a) => (
        <AlertToast key={a.id} alert={a} now={now} onClose={() => queue.close(a.id)} onRead={() => markRead(a.id)} onExpand={() => expand(a.id)} />
      ))}
    </div>
  )
}
