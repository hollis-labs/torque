import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FixtureSource } from './fixture-source'
import { isMessageLifecycle, isStateChanged, KINDS, MESSAGE_KINDS } from './types'
import { parseConsoleEvent, parseGapInfo } from './parse'
import { consoleReducer, initialConsoleState } from './reducer'
import { createConsoleStore } from './store'

describe('envelope v1.1 parsing', () => {
  it('parses optional nested blocks without a session', () => {
    const e = parseConsoleEvent({
      id: 'x', kind: 'fleet.node.down', recorded_at: '2026-10-16T01:00:00Z', epoch: 7, cursor: 99,
      subject: { type: 'node', id: 'w1' },
      scope: { host: 'hub', attempt: 2, bogus: { a: 1 } },
      corr: { trace_id: 't', causation_id: 'c', thread_id: 'th', depth: 2 },
      actor: { kind: 'service', id: 'urn:x', on_behalf_of: 'u', verified: 'token' },
      class: { category: 'fleet', severity: 'error', sensitivity: 'pii', visibility: 'mesh', payload_schema: 's@1', labels: ['a'], sanitized: true, redactions: 3 },
    })!
    expect(e.sessionId).toBeUndefined()
    expect(e.subject).toEqual({ type: 'node', id: 'w1' })
    expect(e.subjects).toEqual([{ type: 'node', id: 'w1' }])
    expect(e.scope).toEqual({ host: 'hub', attempt: 2 })
    expect(e.corr).toMatchObject({ traceId: 't', causationId: 'c', threadId: 'th', depth: 2 })
    expect(e.threadId).toBe('th')
    expect(e.actor).toEqual({ kind: 'service', id: 'urn:x', onBehalfOf: 'u', verified: 'token' })
    expect(e.class).toMatchObject({ category: 'fleet', pii: true, visibility: 'mesh', payloadSchema: 's@1', redactions: 3 })
    expect(e.severity).toBe('error')
    expect(e.epoch).toBe('7')
    expect(e.cursor).toBe('99')
    expect(e.recordedAt).toBe(Date.parse('2026-10-16T01:00:00Z'))
    expect(e.extra).toEqual({})
  })

  it('leaves v1 events untouched and top-level severity wins over class.severity', () => {
    const e = parseConsoleEvent({ id: 'a', kind: 'k', session_id: 's1', severity: 'warning', class: { severity: 'error' } })!
    expect(e.severity).toBe('warning')
    expect(e.sessionId).toBe('s1')
    expect(e.scope).toBeUndefined()
    expect(parseConsoleEvent({ id: 'b', kind: 'k', actor: 'nope', scope: 3 })!.actor).toBeUndefined()
  })

  it('parseGapInfo tolerates empty frames', () => {
    expect(parseGapInfo(null)).toEqual({})
    expect(parseGapInfo({ reason: 'overflow', earliest_available: '5', high_water: 9 })).toEqual({ reason: 'overflow', earliestAvailable: '5' })
  })
})

describe('draft kind families in the fixture', () => {
  it('emits a sample of every family; messages carry body_ref and never a body', async () => {
    const snap = await new FixtureSource({ intervalMs: 0 }).snapshot()
    const kinds = new Set(snap.events.map((e) => e.kind))
    for (const k of [
      'task.state_changed', 'run.state_changed', 'session.state_changed', 'checkpoint.state_changed', 'sprint.state_changed',
      'phase.state_changed', 'claim.state_changed', 'delivery.state_changed', KINDS.gateEvaluated, KINDS.phaseStarted,
      KINDS.phaseCompleted, KINDS.progressCheckpoint, KINDS.dataEmitted, KINDS.stepExecuted, KINDS.toolCallStarted,
      KINDS.toolCallFinished, ...MESSAGE_KINDS, KINDS.approvalRequested, KINDS.approvalResolved, KINDS.fleetNodeDown,
      KINDS.fleetNodeUp, KINDS.fleetNodeRestarted, KINDS.fleetNodeDegraded, KINDS.alertRaised, KINDS.alertCleared,
    ]) expect(kinds.has(k), k).toBe(true)
    const msgs = snap.events.filter((e) => isMessageLifecycle(e.kind))
    expect(msgs).toHaveLength(MESSAGE_KINDS.length)
    for (const m of msgs) {
      expect(m.payload.body_ref).toBeTruthy()
      expect(m.payload).not.toHaveProperty('body')
      expect(m.sessionId).toBeUndefined()
    }
    expect(snap.events.filter((e) => isStateChanged(e.kind)).length).toBeGreaterThanOrEqual(8)
    expect(snap.epoch).toBeTruthy()
  })
})

describe('cursor + epoch resume model', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('reducer resets the cursor on an epoch change but keeps id-merged events', () => {
    let s = consoleReducer(initialConsoleState(), { type: 'snapshot', events: [], cursor: '50', epoch: 'e1', at: 1 })
    expect(s.epoch).toBe('e1')
    s = consoleReducer(s, { type: 'snapshot', events: [], cursor: '2', epoch: 'e2', at: 2 })
    expect([s.epoch, s.cursor]).toEqual(['e2', '2'])
    s = consoleReducer(s, { type: 'gap', info: { reason: 'r' } })
    expect(s.gap).toEqual({ reason: 'r' })
  })

  it('a detailed gap replays from scratch; an empty gap resumes from the cursor', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 3 })
    const snap = vi.spyOn(source, 'snapshot')
    const store = createConsoleStore({ source, resyncMs: 0 })
    store.start()
    await vi.advanceTimersByTimeAsync(0)
    snap.mockClear()
    source.simulateGap()
    await vi.advanceTimersByTimeAsync(0)
    expect(snap.mock.calls[0][0]?.since).toBe('3')
    source.simulateGap({ reason: 'overflow', earliestAvailable: '1', highWater: '3' })
    await vi.advanceTimersByTimeAsync(0)
    expect(snap.mock.calls[1][0]?.since).toBeNull()
    expect(store.getState().gap).toBeNull()
    store.stop()
  })

  it('an epoch restart refetches and adopts the new epoch', async () => {
    const source = new FixtureSource({ intervalMs: 0 })
    const store = createConsoleStore({ source, resyncMs: 0 })
    store.start()
    await vi.advanceTimersByTimeAsync(0)
    const before = store.getState().epoch
    expect(before).toBeTruthy()
    source.restartEpoch()
    await vi.advanceTimersByTimeAsync(0)
    source.tick()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.getState().epoch).not.toBe(before)
    expect(store.getState().cursor).toBe('1')
    expect(store.getState().events.length).toBeGreaterThan(1)
    store.stop()
  })
})
