import { consoleReducer, initialConsoleState, type ConsoleReducerAction, type ConsoleState } from './reducer'
import type { ConsoleSource } from './source'
import type { ConsoleEvent } from './types'

export interface ConsoleStoreOptions {
  source: ConsoleSource
  /** Safety-net resync interval; 0 disables. Streams carry changes only, so a slow snapshot heals gaps. */
  resyncMs?: number
  limit?: number
  now?: () => number
}

export interface ConsoleStore {
  getState(): ConsoleState
  subscribe(listener: () => void): () => void
  /** Idempotent: a second start() while running is a no-op (StrictMode double-mount safe). */
  start(): void
  stop(): void
  /** Force a snapshot merge now. */
  resync(): Promise<void>
  readonly source: ConsoleSource
}

/**
 * External store (useSyncExternalStore-compatible) that owns the single source
 * subscription. Ordering follows useFleetAgents: subscribe first, buffer stream
 * events until a snapshot has landed, then merge; resync on every 'live' and on
 * every gap.
 */
export function createConsoleStore(opts: ConsoleStoreOptions): ConsoleStore {
  const { source, resyncMs = 20_000, limit, now = Date.now } = opts
  let state = initialConsoleState()
  const listeners = new Set<() => void>()
  let unsubscribe: (() => void) | null = null
  let timer: ReturnType<typeof setInterval> | null = null
  let generation = 0
  let buffer: { events: ConsoleEvent[]; cursor?: string; epoch?: string }[] = []

  const dispatch = (a: ConsoleReducerAction) => {
    const next = consoleReducer(state, a)
    if (next === state) return
    state = next
    listeners.forEach((l) => l())
  }

  /** `fresh` drops the resume cursor: used after an epoch change or a gap that pruned our position. */
  const run = async (fresh: boolean) => {
    const gen = generation
    try {
      const snap = await source.snapshot({ since: fresh ? null : state.cursor })
      if (gen !== generation) return
      dispatch({ type: 'snapshot', events: snap.events, cursor: snap.cursor, epoch: snap.epoch, at: now(), limit })
      const pending = buffer
      buffer = []
      for (const b of pending) dispatch({ type: 'events', events: b.events, cursor: b.cursor, epoch: b.epoch, at: now(), limit })
    } catch (e) {
      if (gen === generation) dispatch({ type: 'error', message: e instanceof Error ? e.message : 'Console source unavailable' })
    }
  }

  const resync = () => run(false)

  /** True when incoming events belong to a different log epoch than our cursor. */
  const epochChanged = (events: ConsoleEvent[], epoch?: string) => {
    const e = epoch ?? events.find((x) => x.epoch)?.epoch
    return !!e && state.epoch !== null && e !== state.epoch
  }

  return {
    source,
    getState: () => state,
    subscribe(l) {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    start() {
      if (unsubscribe) return
      generation++
      buffer = []
      unsubscribe = source.subscribe(
        {
          onEvents(events, cursor, epoch) {
            if (!state.ready) buffer.push({ events, cursor, epoch })
            else {
              const changed = epochChanged(events, epoch)
              dispatch({ type: 'events', events, cursor, epoch, at: now(), limit })
              // Cursor continuity is broken: events are merged by id, then refetch from scratch.
              if (changed) void run(true)
            }
          },
          onStatus(status) {
            dispatch({ type: 'status', status })
            if (status === 'live') void resync()
          },
          onGap(info) {
            // With detail, our cursor may be pruned (earliest_available) so replay from scratch;
            // without detail, fall back to the plain resync (id-merge heals it).
            dispatch({ type: 'gap', info: info ?? {} })
            void run(!!info && (info.earliestAvailable !== undefined || info.reason !== undefined))
          },
        },
        { cursor: state.cursor },
      )
      void resync()
      if (resyncMs > 0) timer = setInterval(() => void resync(), resyncMs)
    },
    stop() {
      generation++
      unsubscribe?.()
      unsubscribe = null
      if (timer) clearInterval(timer)
      timer = null
      buffer = []
    },
    resync,
  }
}
