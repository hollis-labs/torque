import type { Recipient, SendRequest } from './types'
import { parseUrn } from '@/lib/messaging'

export const MAX_BODY = 4000

/** Returns a human reason the request must not be sent, or null when it is fine. */
export function validateSend(req: SendRequest): string | null {
  const to = req.to
  if (!to?.address || !parseUrn(to.address)) return 'No resolvable recipient. Pick an agent with a registered address.'
  if (!req.body.trim()) return 'Write a message first.'
  if (req.body.length > MAX_BODY) return `Message is over ${MAX_BODY} characters.`
  if (to.broadcast && !req.confirmedBroadcast) return `Confirm the broadcast to ${to.label} before sending.`
  return null
}

export function isBroadcast(r: Recipient): boolean {
  return !!r.broadcast || r.kind === 'group'
}

/** Key a nudge counts against: the agent slug when known, else the address. */
export const nudgeKey = (r: Recipient) => r.agentId ?? r.address

export interface NudgeLimiter {
  /** ms until another nudge is allowed (0 = allowed now). */
  wait(r: Recipient): number
  record(r: Recipient): void
}

/** At most one nudge per agent per window (default 2 minutes). */
export function createNudgeLimiter(opts: { windowMs?: number; now?: () => number } = {}): NudgeLimiter {
  const windowMs = opts.windowMs ?? 120_000
  const now = opts.now ?? Date.now
  const last = new Map<string, number>()
  return {
    wait(r) {
      const t = last.get(nudgeKey(r))
      return t === undefined ? 0 : Math.max(0, t + windowMs - now())
    },
    record(r) {
      last.set(nudgeKey(r), now())
    },
  }
}
