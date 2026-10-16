import type { SourceStatus } from './source'
import type { ConsoleCursor, ConsoleEvent, GapInfo } from './types'

export interface ConsoleState {
  /** Ascending by (ts, id). Bounded by `limit`. */
  events: ConsoleEvent[]
  /** id -> event, same members as `events`; the dedupe index. */
  byId: Record<string, ConsoleEvent>
  cursor: ConsoleCursor | null
  /** Log epoch the cursor belongs to (DRAFT envelope v1.1), when the source reports one. */
  epoch: string | null
  /** Last gap frame seen and not yet healed by a snapshot. */
  gap: GapInfo | null
  status: SourceStatus
  /** False until the first snapshot lands. */
  ready: boolean
  error: string | null
  /** Epoch ms of the last applied snapshot or event. */
  updatedAt: number | null
}

export const DEFAULT_EVENT_LIMIT = 500

export function initialConsoleState(): ConsoleState {
  return { events: [], byId: {}, cursor: null, epoch: null, gap: null, status: 'connecting', ready: false, error: null, updatedAt: null }
}

export type ConsoleReducerAction =
  | { type: 'events'; events: ConsoleEvent[]; cursor?: ConsoleCursor | null; epoch?: string | null; at: number; limit?: number }
  | { type: 'snapshot'; events: ConsoleEvent[]; cursor: ConsoleCursor | null; epoch?: string | null; at: number; limit?: number }
  | { type: 'gap'; info: GapInfo }
  | { type: 'status'; status: SourceStatus }
  | { type: 'error'; message: string | null }
  | { type: 'reset' }

function cmp(a: ConsoleEvent, b: ConsoleEvent): number {
  return a.ts - b.ts || a.id.localeCompare(b.id)
}

/** Epoch from the explicit value, else from the newest event that carries one. */
export function epochOf(events: ConsoleEvent[], explicit?: string | null): string | null {
  if (explicit) return explicit
  for (let i = events.length - 1; i >= 0; i--) if (events[i].epoch) return events[i].epoch!
  return null
}

/** Merge by id (incoming wins), keep sorted, cap to the newest `limit`. */
function merge(state: ConsoleState, incoming: ConsoleEvent[], limit: number): Pick<ConsoleState, 'events' | 'byId'> {
  if (incoming.length === 0) return { events: state.events, byId: state.byId }
  const byId = { ...state.byId }
  for (const e of incoming) byId[e.id] = e
  let events = Object.values(byId).sort(cmp)
  if (events.length > limit) {
    events = events.slice(events.length - limit)
    const kept: Record<string, ConsoleEvent> = {}
    for (const e of events) kept[e.id] = e
    return { events, byId: kept }
  }
  return { events, byId }
}

/**
 * Pure reducer. A snapshot MERGES with what the stream already delivered
 * (union by id) rather than replacing it: replacing would drop events that
 * arrived while the snapshot was in flight, or events from other machines the
 * snapshot did not cover. Events are never removed except by the size cap.
 */
export function consoleReducer(state: ConsoleState, action: ConsoleReducerAction): ConsoleState {
  switch (action.type) {
    case 'events': {
      const epoch = epochOf(action.events, action.epoch)
      // A new epoch means the old cursor is meaningless; ids still dedupe, so keep events.
      const changed = epoch !== null && state.epoch !== null && epoch !== state.epoch
      return {
        ...state,
        ...merge(state, action.events, action.limit ?? DEFAULT_EVENT_LIMIT),
        cursor: changed ? (action.cursor ?? null) : (action.cursor ?? state.cursor),
        epoch: epoch ?? state.epoch,
        updatedAt: action.events.length ? action.at : state.updatedAt,
      }
    }
    case 'snapshot': {
      const epoch = epochOf(action.events, action.epoch)
      const changed = epoch !== null && state.epoch !== null && epoch !== state.epoch
      return {
        ...state,
        ...merge(state, action.events, action.limit ?? DEFAULT_EVENT_LIMIT),
        cursor: changed ? action.cursor : (action.cursor ?? state.cursor),
        epoch: epoch ?? state.epoch,
        gap: null,
        ready: true,
        error: null,
        updatedAt: action.at,
      }
    }
    case 'gap':
      return { ...state, gap: action.info }
    case 'status':
      return { ...state, status: action.status }
    case 'error':
      return { ...state, error: action.message }
    case 'reset':
      return initialConsoleState()
  }
}
