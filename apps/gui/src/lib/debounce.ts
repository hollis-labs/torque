/**
 * Coalesces bursts of calls into one trailing call: `schedule()` (re)starts
 * a `delayMs` quiet timer, and `maxWaitMs` caps how long a steady stream of
 * calls can postpone it, so constant activity still refreshes. Used to turn
 * SSE event bursts into at most one refetch per window (CW-20261001-0023).
 */
export interface TrailingDebounce {
  schedule: () => void
  cancel: () => void
}

export function createTrailingDebounce(fn: () => void, delayMs: number, maxWaitMs = delayMs * 4): TrailingDebounce {
  let timer: ReturnType<typeof setTimeout> | null = null
  let firstScheduledAt: number | null = null

  const fire = () => {
    timer = null
    firstScheduledAt = null
    fn()
  }

  return {
    schedule() {
      const now = Date.now()
      if (firstScheduledAt === null) firstScheduledAt = now
      if (timer !== null) clearTimeout(timer)
      const untilMax = firstScheduledAt + maxWaitMs - now
      timer = setTimeout(fire, Math.max(0, Math.min(delayMs, untilMax)))
    },
    cancel() {
      if (timer !== null) clearTimeout(timer)
      timer = null
      firstScheduledAt = null
    },
  }
}

/** Trailing debounce window for SSE-driven refetches. */
export const EVENT_REFETCH_DEBOUNCE_MS = 1500
