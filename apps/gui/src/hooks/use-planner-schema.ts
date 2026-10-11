import { useEffect, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import type { PlannerDb, PlannerSchemaInfo } from '@/lib/types'

// Schemas change only with a release, so each API client asks once per database.
const cache = new WeakMap<object, Map<PlannerDb, Promise<PlannerSchemaInfo>>>()

/** The server's schema for one planner database, or null while it loads or if it failed. */
export function usePlannerSchema(db: PlannerDb): PlannerSchemaInfo | null {
  const api = useApi()
  const [loaded, setLoaded] = useState<{ db: PlannerDb; schema: PlannerSchemaInfo } | null>(null)

  useEffect(() => {
    let live = true
    let perApi = cache.get(api)
    if (!perApi) cache.set(api, (perApi = new Map()))
    let p = perApi.get(db)
    if (!p) {
      p = api.plannerSchema(db)
      perApi.set(db, p)
      p.catch(() => perApi.delete(db)) // retry next time after a failure
    }
    p.then((schema) => live && setLoaded({ db, schema })).catch(() => undefined)
    return () => {
      live = false
    }
  }, [api, db])

  return loaded && loaded.db === db ? loaded.schema : null
}
