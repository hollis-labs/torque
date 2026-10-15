import { useEffect, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import { parseFleetAgents, type FleetAgent } from '@/lib/fleet-agents'

const POLL_MS = 3_000

/** Polls the activity snapshot. Keeps the last good rows on a failed poll and reports the error. */
export function useFleetAgents(): { agents: FleetAgent[] | null; error: string | null; now: number } {
  const api = useApi()
  const [agents, setAgents] = useState<FleetAgent[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    let live = true
    const poll = async () => {
      try {
        const body = await api.getActivityAgents()
        if (!live) return
        setAgents(parseFleetAgents(body))
        setError(null)
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : 'Activity source unavailable')
      }
      if (live) setNow(Date.now())
    }
    void poll()
    const t = setInterval(() => void poll(), POLL_MS)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [api])

  return { agents, error, now }
}
