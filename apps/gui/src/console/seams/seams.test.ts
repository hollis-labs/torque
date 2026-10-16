import { describe, expect, it, vi } from 'vitest'
import { actionFromOffer, createActionBus, type ActionHandler } from './action-bus'
import { MockIdentityConfirm } from './identity-confirm'
import { MemoryStateStore, effectiveStatus } from './state-store'

describe('MemoryStateStore', () => {
  it('applies ops, notifies subscribers, and expires snoozes', async () => {
    let t = 1000
    const store = new MemoryStateStore({ now: () => t })
    const seen = vi.fn()
    store.subscribe(seen)
    await store.apply({ type: 'snooze', key: 'e1', until: 2000 })
    expect(seen).toHaveBeenCalledTimes(1)
    const [row] = await store.load()
    expect(effectiveStatus(row, 1500)).toBe('snoozed')
    expect(effectiveStatus(row, 2000)).toBe('unread')
    await store.apply({ type: 'dismiss', key: 'e1' })
    const read = await store.apply({ type: 'read', key: 'e1' })
    expect(read.status).toBe('dismissed')
    t = 3000
    expect((await store.apply({ type: 'reopen', key: 'e1' })).status).toBe('unread')
  })
})

describe('ActionBus + IdentityConfirm', () => {
  const offer = { id: 'approve', label: 'Approve', kind: 'approve', requiresConfirm: true, scope: ['agent:apply'] }

  it('runs the handler with the grant when step-up succeeds', async () => {
    const bus = createActionBus({ identity: new MockIdentityConfirm({ mode: 'auto-grant' }) })
    const handler = vi.fn<ActionHandler>(async () => ({ status: 'ok' }))
    bus.register('approve', handler)
    const seen = vi.fn()
    bus.subscribe(seen)
    const res = await bus.dispatch(actionFromOffer(offer, { eventId: 'e1' }))
    expect(res.status).toBe('ok')
    expect(res.confirmation?.scope).toEqual(['agent:apply'])
    expect(handler.mock.calls[0][1].confirmation?.token).toBeTruthy()
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it('never runs the handler when step-up is denied', async () => {
    const bus = createActionBus({ identity: new MockIdentityConfirm({ mode: 'auto-deny' }) })
    const handler = vi.fn()
    bus.register('approve', handler)
    const res = await bus.dispatch(actionFromOffer(offer, {}))
    expect(res.status).toBe('denied')
    expect(handler).not.toHaveBeenCalled()
  })

  it('treats an expired grant as denied', async () => {
    let t = 0
    const identity = new MockIdentityConfirm({ now: () => t, defaultTtlMs: 10 })
    const slow = { confirm: async (r: Parameters<typeof identity.confirm>[0]) => { const g = await identity.confirm(r); t = 100; return g } }
    const bus = createActionBus({ identity: slow, now: () => t })
    const handler = vi.fn()
    bus.register('approve', handler)
    expect((await bus.dispatch(actionFromOffer(offer, {}))).status).toBe('denied')
    expect(handler).not.toHaveBeenCalled()
  })

  it('skips step-up for unconfirmed actions, reports unhandled and handler errors', async () => {
    const identity = new MockIdentityConfirm({ mode: 'auto-deny' })
    const bus = createActionBus({ identity })
    expect((await bus.dispatch({ kind: 'nope' })).status).toBe('unhandled')
    bus.register('dismiss', () => undefined)
    expect((await bus.dispatch({ kind: 'dismiss' })).status).toBe('ok')
    bus.register('boom', () => { throw new Error('bad') })
    expect(await bus.dispatch({ kind: 'boom' })).toMatchObject({ status: 'error', message: 'bad' })
  })

  it('manual mode queues for a dialog to resolve', async () => {
    const identity = new MockIdentityConfirm({ mode: 'manual' })
    const p = identity.confirm({ what: { action: 'a', title: 'A' }, scope: ['s'] })
    expect(identity.pending()).toHaveLength(1)
    identity.resolve(identity.pending()[0].id, 'cancelled')
    expect((await p).outcome).toBe('cancelled')
    expect(identity.pending()).toHaveLength(0)
  })
})
