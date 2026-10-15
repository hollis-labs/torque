import { useEffect, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import { applyFleetEvent, parseFleetAgents, type FleetAgent } from '@/lib/fleet-agents'

/** The stream carries changes only, so a slow snapshot heals anything missed (a gap, a reconnect, the open race). */
const RESYNC_MS = 20_000
/** Re-renders "ago" labels between events. */
const CLOCK_MS = 5_000

/**
 * Live agents: the stream is opened first, then the snapshot is fetched, and
 * only events after the snapshot are applied: the snapshot already reflects
 * anything that arrived while it was in flight, and the resync covers the
 * sliver left. Keeps the last good rows on a failed fetch and reports the error.
 */
export function useFleetAgents(): { agents: FleetAgent[] | null; error: string | null; now: number; live: boolean } {
  const api = useApi()
  const [agents, setAgents] = useState<FleetAgent[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    let active = true
    let ready = false
    const sync = async () => {
      try {
        const body = await api.getActivityAgents()
        if (!active) return
        setAgents(parseFleetAgents(body))
        setError(null)
        ready = true
      } catch (e) {
        if (active) setError(e instanceof Error ? e.message : 'Activity source unavailable')
      }
    }
    const unsubscribe = api.subscribeActivity(
      (kind, data) => {
        if (kind === 'gap') {
          ready = false
          void sync()
          return
        }
        if (!ready) return
        setAgents((prev) => (prev ? applyFleetEvent(prev, kind, data) : prev))
        setNow(Date.now())
      },
      (status) => {
        setLive(status === 'live')
        if (status === 'live') {
          ready = false
          void sync()
        }
      },
    )
    void sync()
    const resync = setInterval(() => void sync(), RESYNC_MS)
    const clock = setInterval(() => setNow(Date.now()), CLOCK_MS)
    return () => {
      active = false
      unsubscribe()
      clearInterval(resync)
      clearInterval(clock)
    }
  }, [api])

  return { agents, error, now, live }
}
