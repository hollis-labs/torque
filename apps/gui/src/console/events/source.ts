import type { ConsoleCursor, ConsoleEvent, ConsoleSnapshot, GapInfo } from './types'

export type SourceStatus = 'connecting' | 'live' | 'error'

export interface SubscribeHandlers {
  /** New events, in arrival order. `cursor` is the replay position after them, if the source has one. */
  onEvents(events: ConsoleEvent[], cursor?: ConsoleCursor, epoch?: string): void
  onStatus(status: SourceStatus): void
  /** The source knows it dropped events (server gap marker, buffer overflow): the store resyncs from a snapshot. */
  /** Optional detail (DRAFT gap frame: reason, earliest_available, high_water). */
  onGap(info?: GapInfo): void
}

export interface SnapshotOptions {
  /** Replay cursor: when the source supports it, return only events after this position. */
  since?: ConsoleCursor | null
  signal?: AbortSignal
}

/**
 * The one seam between the Console and any event feed. Adapters (live feeds,
 * fixtures, and later the real go-runtime-events plane) implement this; nothing
 * else in the Console knows where events come from.
 *
 * Contract:
 * - `subscribe` is called BEFORE `snapshot` so nothing falls between them; the
 *   store buffers stream events until the snapshot lands, then merges by id.
 * - `status: 'live'` fires on every (re)open; the store resyncs on it.
 * - Sources never throw from `subscribe`; failures are reported via onStatus.
 */
export interface ConsoleSource {
  readonly id: string
  snapshot(opts?: SnapshotOptions): Promise<ConsoleSnapshot>
  subscribe(handlers: SubscribeHandlers, opts?: { cursor?: ConsoleCursor | null }): () => void
}
