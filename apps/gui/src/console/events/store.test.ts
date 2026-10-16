import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { consoleReducer, initialConsoleState } from './reducer'
import { FixtureSource } from './fixture-source'
import { createConsoleStore } from './store'
import type { ConsoleSource, SubscribeHandlers } from './source'
import type { ConsoleEvent } from './types'

const ev = (id: string, ts: number, over: Partial<ConsoleEvent> = {}): ConsoleEvent => ({
  id, ts, kind: 'k', source: { id: 's' }, subjects: [], severity: 'info', payload: {}, actions: [], extra: {}, ...over,
})

describe('consoleReducer', () => {
  it('dedupes by id (latest wins), sorts by ts, merges snapshots instead of replacing', () => {
    let s = consoleReducer(initialConsoleState(), { type: 'events', events: [ev('b', 2), ev('a', 1)], at: 1 })
    s = consoleReducer(s, { type: 'events', events: [ev('a', 1, { severity: 'error' })], at: 2 })
    expect(s.events.map((e) => e.id)).toEqual(['a', 'b'])
    expect(s.byId.a.severity).toBe('error')
    s = consoleReducer(s, { type: 'snapshot', events: [ev('c', 3)], cursor: '9', at: 3 })
    expect(s.events.map((e) => e.id)).toEqual(['a', 'b', 'c'])
    expect(s.ready).toBe(true)
    expect(s.cursor).toBe('9')
  })

  it('caps to the newest events', () => {
    const s = consoleReducer(initialConsoleState(), { type: 'events', events: [ev('a', 1), ev('b', 2), ev('c', 3)], at: 1, limit: 2 })
    expect(s.events.map((e) => e.id)).toEqual(['b', 'c'])
    expect(s.byId.a).toBeUndefined()
  })
})

describe('createConsoleStore', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('subscribes before the snapshot, buffers early stream events, and merges them', async () => {
    const order: string[] = []
    let h!: SubscribeHandlers
    let release!: () => void
    const source: ConsoleSource = {
      id: 't',
      subscribe(handlers) {
        order.push('subscribe')
        h = handlers
        return () => order.push('unsub')
      },
      snapshot() {
        order.push('snapshot')
        return new Promise((r) => { release = () => r({ events: [ev('snap', 1)], cursor: '1' }) })
      },
    }
    const store = createConsoleStore({ source, resyncMs: 0 })
    store.start()
    h.onEvents([ev('early', 2)], '2')
    expect(store.getState().events).toHaveLength(0)
    release()
    await vi.advanceTimersByTimeAsync(0)
    expect(order.slice(0, 2)).toEqual(['subscribe', 'snapshot'])
    expect(store.getState().events.map((e) => e.id)).toEqual(['snap', 'early'])
    h.onEvents([ev('late', 3)])
    expect(store.getState().events).toHaveLength(3)
    store.stop()
    expect(order).toContain('unsub')
  })

  it('start is idempotent and a gap triggers one more snapshot', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 3, now: () => 1000 })
    const snap = vi.spyOn(source, 'snapshot')
    const sub = vi.spyOn(source, 'subscribe')
    const store = createConsoleStore({ source, resyncMs: 0, now: () => 1000 })
    store.start()
    store.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(sub).toHaveBeenCalledTimes(1)
    const before = snap.mock.calls.length
    source.simulateGap()
    await vi.advanceTimersByTimeAsync(0)
    expect(snap.mock.calls.length).toBe(before + 1)
    expect(store.getState().ready).toBe(true)
    expect(store.getState().events).toHaveLength(3)
    source.tick()
    expect(store.getState().events).toHaveLength(4)
    store.stop()
  })

  it('keeps last good events and reports an error when a snapshot fails', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 2 })
    const store = createConsoleStore({ source, resyncMs: 0 })
    store.start()
    await vi.advanceTimersByTimeAsync(0)
    vi.spyOn(source, 'snapshot').mockRejectedValueOnce(new Error('down'))
    await store.resync()
    expect(store.getState().error).toBe('down')
    expect(store.getState().events).toHaveLength(2)
    store.stop()
  })
})

describe('FixtureSource', () => {
  it('is deterministic per seed and supports cursor replay', async () => {
    const a = new FixtureSource({ seed: 5, intervalMs: 0, now: () => 10 })
    const b = new FixtureSource({ seed: 5, intervalMs: 0, now: () => 10 })
    const sa = await a.snapshot()
    expect(sa.events.map((e) => e.kind)).toEqual((await b.snapshot()).events.map((e) => e.kind))
    expect(new Set(sa.events.map((e) => e.kind)).size).toBeGreaterThan(4)
    a.tick()
    const after = await a.snapshot({ since: sa.cursor })
    expect(after.events).toHaveLength(1)
  })
})
