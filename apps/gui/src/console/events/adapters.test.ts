import { describe, expect, it, vi } from 'vitest'
import { ActivityStreamSource, CompositeSource, TorqueEventsSource } from './adapters'
import { FixtureSource } from './fixture-source'

describe('adapters', () => {
  it('ActivityStreamSource maps go-runtime-events envelopes and gap markers', async () => {
    let emit!: (k: string, d: unknown) => void
    const api = {
      getActivityAgents: async () => ({ agents: [{ id: 's1', state: 'waiting', waiting_for: 'approval', cwd: '/a/b' }] }),
      subscribeActivity: (on: (k: string, d: unknown) => void) => { emit = on; return () => undefined },
    }
    const src = new ActivityStreamSource(api)
    const snap = await src.snapshot()
    expect(snap.events[0]).toMatchObject({ kind: 'agent.state', payload: { state: 'waiting', waitingFor: 'approval' } })
    const onEvents = vi.fn()
    const onGap = vi.fn()
    src.subscribe({ onEvents, onStatus: vi.fn(), onGap })
    emit('session.idle', { session_id: 's1', time: '2026-10-16T00:00:00Z', payload: { state: 'idle' } })
    emit('gap', {})
    emit('session.idle', null)
    expect(onEvents).toHaveBeenCalledTimes(1)
    expect(onEvents.mock.calls[0][0][0]).toMatchObject({ kind: 'session.idle', subjects: [{ type: 'agent', id: 's1' }] })
    expect(onGap).toHaveBeenCalledTimes(1)
  })

  it('TorqueEventsSource wraps domain events', () => {
    let emit!: (e: { type: string; data: Record<string, unknown> }) => void
    const src = new TorqueEventsSource({ subscribeEvents: (on) => { emit = on; return () => undefined } })
    const onEvents = vi.fn()
    src.subscribe({ onEvents, onStatus: vi.fn(), onGap: vi.fn() })
    emit({ type: 'task.updated', data: { id: 'T1' } })
    expect(onEvents.mock.calls[0][0][0]).toMatchObject({ kind: 'torque.task.updated', subjects: [{ type: 'task', id: 'T1' }] })
  })

  it('TorqueEventsSource reads the outbox frame: legacy shape on top, envelope alongside', () => {
    let emit!: (e: { type: string; data: Record<string, unknown>; envelope?: unknown }) => void
    const src = new TorqueEventsSource({ subscribeEvents: (on) => { emit = on; return () => undefined } })
    const onEvents = vi.fn()
    src.subscribe({ onEvents, onStatus: vi.fn(), onGap: vi.fn() })
    emit({
      type: 'task.transitioned',
      data: { task_id: 'CW-1', status: 'doing', from: 'todo', to: 'doing', payload: { from: 'todo', to: 'doing' }, trigger: { kind: 'human', ref: 'http' } },
      envelope: { kind: 'task.state_changed', id: 'evt_1' },
    })
    expect(onEvents).toHaveBeenCalledTimes(1)
    expect(onEvents.mock.calls[0][0][0]).toMatchObject({
      kind: 'torque.task.transitioned',
      subjects: [{ type: 'task', id: 'CW-1' }],
      payload: { status: 'doing', from: 'todo', to: 'doing' },
    })
  })

  it('TorqueEventsSource turns a gap frame into a gap signal, not an event', () => {
    let emit!: (e: { type: string; data: Record<string, unknown> }) => void
    const src = new TorqueEventsSource({ subscribeEvents: (on) => { emit = on; return () => undefined } })
    const onEvents = vi.fn()
    const onGap = vi.fn()
    src.subscribe({ onEvents, onStatus: vi.fn(), onGap })
    emit({ type: 'gap', data: { reason: 'pruned', earliest_available: '1:7', high_water: '1:40' } })
    expect(onEvents).not.toHaveBeenCalled()
    expect(onGap).toHaveBeenCalledWith({ reason: 'pruned', earliestAvailable: '1:7', highWater: '1:40' })
  })

  it('CompositeSource merges snapshots and survives one failing source', async () => {
    const good = new FixtureSource({ intervalMs: 0, initial: 2 })
    const bad = { id: 'bad', snapshot: async () => { throw new Error('x') }, subscribe: () => () => undefined }
    const snap = await new CompositeSource([good, bad]).snapshot()
    expect(snap.events).toHaveLength(2)
  })
})
