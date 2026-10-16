// Wiring hooks: the HitlClient context and the live request list.
import { createContext, useContext, useEffect, useMemo, useState } from 'react'
import type { HitlClient } from './client'
import type { HitlRequest } from './types'

export const HitlClientContext = createContext<HitlClient | null>(null)

export function useHitlClient(): HitlClient {
  const c = useContext(HitlClientContext)
  if (!c) throw new Error('useHitlClient must be used inside <HitlProvider>')
  return c
}

export function useHitlRequests(client: HitlClient): HitlRequest[] {
  const [items, setItems] = useState<Record<string, HitlRequest>>({})
  useEffect(() => {
    let live = true
    void client.list().then((all) => live && setItems((cur) => ({ ...Object.fromEntries(all.map((r) => [r.id, r])), ...cur })))
    const off = client.subscribe((changed) => setItems((cur) => ({ ...cur, ...Object.fromEntries(changed.map((r) => [r.id, r])) })))
    return () => {
      live = false
      off()
    }
  }, [client])
  return useMemo(() => Object.values(items).sort((a, b) => b.createdAt - a.createdAt), [items])
}

