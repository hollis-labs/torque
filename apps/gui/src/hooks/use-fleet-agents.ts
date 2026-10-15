import { useEffect, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import { applyFleetEvent, parseFleetAgents, trackTransitions, type FleetAgent, type FleetHistory } from '@/lib/fleet-agents'

/** The stream carries changes only, so a slow snapshot heals anything missed (a gap, a reconnect, the open race). */
const RESYNC_MS = 20_000
/** Re-renders "ago" labels between events. */
const CLOCK_MS = 5_000

interface Feed {
  agents: FleetAgent[] | null
  /** What this page has seen change since it opened; the source keeps no history. */
  history: FleetHistory
}

/**
 * Live agents: the stream is opened first, then the snapshot is fetched, and
 * only events after the snapshot are applied: the snapshot already reflects
 * anything that arrived while it was in flight, and the resync covers the
 * sliver left. Keeps the last good rows on a failed fetch and reports the error.
 */
export function useFleetAgents(): { agents: FleetAgent[] | null; history: FleetHistory; error: string | null; now: number; live: boolean } {
  const api = useApi()
  const [feed, setFeed] = useState<Feed>({ agents: null, history: {} })
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    let active = true
    let ready = false
    const commit = (update: (prev: FleetAgent[] | null) => FleetAgent[]) => {
      const at = Date.now()
      setFeed((f) => {
        const next = update(f.agents)
        return { agents: next, history: trackTransitions(f.history, f.agents, next, at) }
      })
      setNow(at)
    }
    const sync = async () => {
      try {
        const body = await api.getActivityAgents()
        if (!active) return
        commit(() => parseFleetAgents(body))
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
        commit((prev) => (prev ? applyFleetEvent(prev, kind, data) : []))
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

  return { agents: feed.agents, history: feed.history, error, now, live }
}
