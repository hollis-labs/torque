import { useEffect, useRef, useState } from 'react'
import { useApi } from './use-api'
import type { SSEEvent } from '@/lib/types'

interface UseSSEResult {
  lastEvent: SSEEvent | null
  connected: boolean
}

export function useSSE(eventTypes?: string[]): UseSSEResult {
  const api = useApi()
  const [lastEvent, setLastEvent] = useState<SSEEvent | null>(null)
  const [connected, setConnected] = useState(false)
  const unsubscribeRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    setConnected(false)

    const unsubscribe = api.subscribeEvents((event) => {
      // If filtering by event types, skip events not in the list
      if (eventTypes && eventTypes.length > 0 && !eventTypes.includes(event.type)) {
        return
      }
      setConnected(true)
      setLastEvent(event)
    })

    unsubscribeRef.current = unsubscribe

    return () => {
      unsubscribe()
      unsubscribeRef.current = null
    }
    // eventTypes intentionally serialized to avoid re-subscribing on every render
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, JSON.stringify(eventTypes)])

  return { lastEvent, connected }
}
