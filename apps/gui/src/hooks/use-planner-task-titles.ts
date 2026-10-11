import { useEffect, useMemo, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import type { PlannerTaskBrief } from '@/lib/types'

/**
 * Live title and status of Torque task ids. Returns a map; an id that is
 * unknown to Torque maps to null, one still loading is absent.
 */
export function usePlannerTaskTitles(ids: string[]): Record<string, PlannerTaskBrief | null> {
  const api = useApi()
  const key = useMemo(() => [...new Set(ids)].sort().join(','), [ids])
  const [loaded, setLoaded] = useState<{ key: string; titles: Record<string, PlannerTaskBrief | null> } | null>(null)

  useEffect(() => {
    if (!key) return
    const ctl = new AbortController()
    api.plannerTaskTitles(key.split(','), ctl.signal)
      .then((titles) => setLoaded({ key, titles }))
      .catch(() => undefined) // a reference without a title still shows its id
    return () => ctl.abort()
  }, [api, key])

  return loaded && loaded.key === key ? loaded.titles : {}
}
