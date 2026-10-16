// One batched, non-destructive unread poll for the Agents tab MailBadge. Badges
// register the address they show (`useAgentUnread`); the store polls
// `MessagingClient.unreadCounts` for all registered addresses in one call, and
// only while at least one badge is mounted. Outside a provider (Console off)
// every hook returns null and nothing runs.
import type { MessagingClient } from './types'

/** Messaging URN for a fleet agent row (provider + process id, path-safe). */
export function fleetAgentAddress(agent: { id: string; provider: string }): string {
  const safe = (s: string) => s.replace(/[^A-Za-z0-9_.:-]/g, '_') || '_'
  return `msg://agent/${safe(agent.provider)}/${safe(agent.id)}`
}

export interface UnreadStore {
  watch(address: string): () => void
  subscribe(listener: () => void): () => void
  get(address: string): number | null
}

export function createUnreadStore(client: MessagingClient, opts: { pollMs?: number; settleMs?: number } = {}): UnreadStore {
  const pollMs = opts.pollMs ?? 30_000
  const settleMs = opts.settleMs ?? 50
  const watchers = new Map<string, number>()
  let counts: Record<string, number> = {}
  const listeners = new Set<() => void>()
  let interval: ReturnType<typeof setInterval> | null = null
  let settle: ReturnType<typeof setTimeout> | null = null
  let inFlight = false

  const refresh = async () => {
    if (inFlight || !client.unreadCounts || watchers.size === 0) return
    inFlight = true
    try {
      const next = await client.unreadCounts([...watchers.keys()])
      const changed = Object.keys(next).some((k) => counts[k] !== next[k])
      if (changed) {
        counts = { ...counts, ...next }
        listeners.forEach((l) => l())
      }
    } catch {
      /* keep the last known counts */
    } finally {
      inFlight = false
    }
  }
  const schedule = () => {
    if (settle) clearTimeout(settle)
    settle = setTimeout(() => void refresh(), settleMs)
  }

  return {
    watch(address) {
      watchers.set(address, (watchers.get(address) ?? 0) + 1)
      if (!interval) interval = setInterval(() => void refresh(), pollMs)
      schedule()
      return () => {
        const n = (watchers.get(address) ?? 1) - 1
        if (n <= 0) watchers.delete(address)
        else watchers.set(address, n)
        if (watchers.size === 0) {
          if (interval) clearInterval(interval)
          if (settle) clearTimeout(settle)
          interval = settle = null
        }
      }
    },
    subscribe(l) {
      listeners.add(l)
      return () => void listeners.delete(l)
    },
    get: (address) => counts[address] ?? null,
  }
}

