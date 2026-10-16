import { useEffect, useState } from 'react'
import type { CardInstance } from '../cards/types'
import { requestFromCard } from './model'
import type { HitlRequest } from './types'

/** Re-renders on an interval that tightens as expiry nears. Returns ms remaining (undefined = no expiry). */
export function useRemaining(expiresAt: number | undefined, active: boolean): number | undefined {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (expiresAt === undefined || !active) return
    const left = expiresAt - Date.now()
    if (left <= 0) return
    const t = setTimeout(() => setNow(Date.now()), left < 3_600_000 ? 1000 : 30_000)
    return () => clearTimeout(t)
  }, [expiresAt, active, now])
  return expiresAt === undefined ? undefined : expiresAt - now
}

export interface HitlCardState {
  req: HitlRequest
  card: CardInstance
  remaining: number | undefined
  /** No further input accepted: closed, or expired locally (the queue does not enforce expiry yet). */
  locked: boolean
  expiredLocally: boolean
}

export function useHitlCard(card: CardInstance): HitlCardState | undefined {
  const req = requestFromCard(card)
  const pending = !!req && req.status === 'pending' && card.phase === 'live'
  const remaining = useRemaining(req?.expiresAt, pending)
  if (!req) return undefined
  const expiredLocally = remaining !== undefined && remaining <= 0
  return { req, card, remaining, expiredLocally, locked: !pending || expiredLocally }
}

