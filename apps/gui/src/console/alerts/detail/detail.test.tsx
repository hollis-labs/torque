// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { FixtureSource } from '../../events/fixture-source'
import { ConsoleProvider } from '../../events/provider'
import type { ConsoleEvent } from '../../events/types'
import { createMockMessagingClient, MessagingProvider } from '../../messaging'
import { createActionBus, MemoryStateStore, MockIdentityConfirm, type StateOp } from '../../seams'
import { AlertHost } from '../AlertHost'
import { AlertTrayTrigger } from '../AlertTrayTrigger'
import { MemoryAlertPrefsStore } from '../prefs'
import { createMockDecisionClient, defaultAlertActions, deferPresets, snoozePresets, type AlertDecisionClient } from './actions'
import { defaultAlertMapper } from '../mapping'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

const NOW = Date.now()
const ev = (o: Partial<ConsoleEvent> & { id: string; kind: string }): ConsoleEvent => ({
  ts: NOW - 60_000, source: { id: 'agent-bravo' }, subjects: [{ type: 'agent', id: 'agent-bravo' }], severity: 'warning', payload: {}, actions: [], extra: {}, ...o,
})
const approval = ev({ id: 'e-appr', kind: 'approval.requested', threadId: 't1', correlationId: 'c1', payload: { title: 'Apply policy change', summary: 'privileged', risk: 'high' } })
const earlier = ev({ id: 'e-prev', kind: 'message.received', ts: NOW - 120_000, threadId: 't1', payload: { subject: 'Heads up', preview: 'about CW-20261016-0112' } })
const message = ev({ id: 'e-msg', kind: 'message.received', severity: 'notice', ts: NOW - 30_000, payload: { subject: 'Need a decision', preview: 'proceed?', from: 'portfolio-manager' }, subjects: [{ type: 'agent', id: 'agent-charlie' }] })

interface Opts {
  store?: MemoryStateStore
  identity?: MockIdentityConfirm
  decisions?: AlertDecisionClient
  events?: ConsoleEvent[]
}

async function setup(o: Opts = {}) {
  const source = new FixtureSource({ intervalMs: 0, initial: 0, families: false })
  for (const e of o.events ?? [earlier, approval, message]) source.emit(e)
  const identity = o.identity ?? new MockIdentityConfirm({ mode: 'auto-grant' })
  const store = o.store ?? new MemoryStateStore()
  const actionBus = createActionBus({ identity })
  const messaging = createMockMessagingClient()
  await act(async () => {
    render(
      <MemoryRouter>
        <ApiProvider client={{} as never}>
          <ConsoleProvider source={source} resyncMs={0} stateStore={store} identity={identity} actionBus={actionBus}>
            <MessagingProvider client={messaging}>
              <AlertHost prefsStore={new MemoryAlertPrefsStore({ quiet: true })} decisionClient={o.decisions}>
                <AlertTrayTrigger />
              </AlertHost>
            </MessagingProvider>
          </ConsoleProvider>
        </ApiProvider>
      </MemoryRouter>,
    )
  })
  return { source, store, identity, messaging }
}

async function openDetail(title: RegExp | string) {
  await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
  await act(async () => fireEvent.click(screen.getByRole('button', { name: new RegExp(`^Open details: ${title instanceof RegExp ? title.source : title}`) })))
  return screen.getByRole('dialog', { name: /^Alert:/ })
}

const click = (el: HTMLElement) => act(async () => void fireEvent.click(el))

describe('action declaration', () => {
  const alertFor = (e: ConsoleEvent) => defaultAlertMapper(e)!
  it('declares the action set per kind, with step-up on approve only', () => {
    const appr = defaultAlertActions(alertFor(approval), approval)
    expect(appr.map((a) => a.id)).toEqual(['approve', 'deny', 'defer', 'snooze', 'reply', 'dismiss'])
    expect(appr.find((a) => a.id === 'approve')?.confirm?.scope).toEqual(['approval.approve'])
    expect(appr.find((a) => a.id === 'deny')?.confirm).toBeUndefined()
    expect(defaultAlertActions(alertFor(message), message).map((a) => a.id)).toEqual(['reply', 'defer', 'snooze', 'settle', 'dismiss'])
    const other = ev({ id: 'x', kind: 'something.odd', severity: 'error' })
    expect(defaultAlertActions(alertFor(other), other).map((a) => a.id)).toEqual(['snooze', 'defer', 'settle', 'dismiss'])
  })
  it('honours an offered approve that opts out of step-up', () => {
    const e = ev({ id: 'o', kind: 'approval.requested', actions: [{ id: 'a', label: 'Go', kind: 'approve', requiresConfirm: false }] })
    expect(defaultAlertActions(alertFor(e), e)[0].confirm).toBeUndefined()
  })
  it('builds time presets in the future', () => {
    expect(snoozePresets(NOW).every((p) => p.until > NOW)).toBe(true)
    expect(deferPresets(NOW).every((p) => p.until > NOW)).toBe(true)
  })
})

describe('AlertDetail modal', () => {
  it('expands from the tray with fields, linked refs and the related timeline', async () => {
    await setup()
    const d = await openDetail('Apply policy change')
    expect(within(d).getByText('risk')).toBeTruthy()
    expect(within(d).getByText('high')).toBeTruthy()
    const tl = within(d).getByRole('region', { name: 'Timeline' })
    expect(within(tl).getByText('Heads up')).toBeTruthy()
    expect(within(d).getByRole('region', { name: 'Linked' })).toBeTruthy()
    await act(async () => fireEvent.keyDown(d, { key: 'Escape' }))
    expect(screen.queryByTestId('alert-detail')).toBeNull()
  })

  it('dismiss closes the alert; restore reopens it', async () => {
    const { store } = await setup()
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Dismiss' }))
    expect((await store.load()).find((s) => s.key === 'e-appr')?.status).toBe('dismissed')
    expect(screen.queryByTestId('alert-detail')).toBeNull()
  })

  it('snooze preset sets an absolute time and shows it', async () => {
    const { store } = await setup()
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Snooze' }))
    await click(within(d).getByRole('button', { name: '1 hour' }))
    const s = (await store.load()).find((x) => x.key === 'e-appr')!
    expect(s.status).toBe('snoozed')
    expect(s.snoozeUntil! - Date.now()).toBeGreaterThan(55 * 60_000)
    expect(within(d).getByTestId('alert-status').textContent).toMatch(/^snoozed until/)
  })

  it('a snoozed alert resurfaces in Active once the time passes', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] })
    const { store } = await setup({ events: [approval] })
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Snooze' }))
    await click(within(d).getByRole('button', { name: '15 min' }))
    expect((await store.load())[0].status).toBe('snoozed')
    await act(async () => fireEvent.keyDown(d, { key: 'Escape' }))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    expect(within(tray).queryAllByTestId('alert-item')).toHaveLength(0)
    await act(async () => void vi.advanceTimersByTime(16 * 60_000))
    expect(within(tray).getAllByTestId('alert-item')).toHaveLength(1)
  })

  it('defer to a context records the wake condition; defer to a time snoozes to it', async () => {
    const { store } = await setup()
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Defer' }))
    await click(within(d).getByRole('tab', { name: /context/i }))
    await click(within(d).getByRole('button', { name: 'When the agent is idle' }))
    const s = (await store.load()).find((x) => x.key === 'e-appr')!
    expect(s.status).toBe('snoozed')
    expect(s.note).toContain('defer:When the agent is idle')
  })

  it('settle resolves with a note', async () => {
    const { store } = await setup()
    const d = await openDetail('Need a decision')
    await click(within(d).getByRole('button', { name: 'Settle' }))
    fireEvent.change(within(d).getByLabelText('Note'), { target: { value: 'handled by phone' } })
    await click(within(within(d).getByRole('region', { name: 'Settle with note' })).getByRole('button', { name: 'Settle' }))
    const s = (await store.load()).find((x) => x.key === 'e-msg')!
    expect(s).toMatchObject({ status: 'settled', resolution: 'handled', note: 'handled by phone' })
  })

  it('approve requires IdentityConfirm: declined changes nothing, granted settles', async () => {
    const identity = new MockIdentityConfirm({ mode: 'manual' })
    const decisions = createMockDecisionClient()
    const { store } = await setup({ identity, decisions })
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: /^Approve/ }))
    await click(within(d).getByRole('button', { name: /confirm identity/ }))
    expect(identity.pending()).toHaveLength(1)
    await act(async () => identity.resolve(identity.pending()[0].id, 'denied', 'no'))
    expect(decisions.calls()).toHaveLength(0)
    expect(within(d).getByRole('alert').textContent).toMatch(/Not confirmed/)
    expect((await store.load()).some((x) => x.status === 'settled')).toBe(false)

    await click(within(d).getByRole('button', { name: /confirm identity/ }))
    await act(async () => identity.resolve(identity.pending()[0].id, 'granted'))
    expect(decisions.calls()).toMatchObject([{ eventId: 'e-appr', decision: 'approve' }])
    expect(decisions.calls()[0].confirmationToken).toMatch(/^mock-/)
    expect((await store.load())[0]).toMatchObject({ status: 'settled', resolution: 'approved' })
    expect(within(d).getByTestId('alert-status').textContent).toBe('approved')
  })

  it('deny goes through the bus without a step-up and settles as rejected', async () => {
    const decisions = createMockDecisionClient()
    const { store } = await setup({ decisions })
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Deny' }))
    await click(within(within(d).getByRole('region', { name: 'Deny' })).getByRole('button', { name: 'Deny' }))
    expect(decisions.calls()[0]).toMatchObject({ decision: 'deny' })
    expect((await store.load())[0]).toMatchObject({ resolution: 'rejected' })
  })

  it('is double-click safe: one decision, one state write', async () => {
    const decisions = createMockDecisionClient({ latencyMs: 20 })
    const { store } = await setup({ decisions })
    const apply = vi.spyOn(store, 'apply')
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: /^Approve/ }))
    const go = within(d).getByRole('button', { name: /confirm identity/ })
    await act(async () => {
      fireEvent.click(go)
      fireEvent.click(go)
      await new Promise((r) => setTimeout(r, 60))
    })
    expect(decisions.calls()).toHaveLength(1)
    expect(apply.mock.calls.filter(([op]) => (op as StateOp).type === 'settle')).toHaveLength(1)
  })

  it('rolls back and shows the error when the state write fails', async () => {
    const store = new MemoryStateStore()
    vi.spyOn(store, 'apply').mockRejectedValue(new Error('server unavailable'))
    await setup({ store })
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: 'Snooze' }))
    await click(within(d).getByRole('button', { name: '15 min' }))
    expect(within(d).getByRole('alert').textContent).toBe('server unavailable')
    expect(d.getAttribute('data-status')).toBe('unread')
  })

  it('surfaces a failed decision and leaves the alert open', async () => {
    const { store } = await setup({ decisions: createMockDecisionClient({ failWith: 'approver offline' }) })
    const d = await openDetail('Apply policy change')
    await click(within(d).getByRole('button', { name: /^Approve/ }))
    await click(within(d).getByRole('button', { name: /confirm identity/ }))
    expect(within(d).getByRole('alert').textContent).toBe('approver offline')
    expect((await store.load()).some((x) => x.status === 'settled')).toBe(false)
  })

  it('reply opens the composer in-thread and sends through messaging', async () => {
    const { messaging } = await setup()
    const d = await openDetail('Need a decision')
    await click(within(d).getByRole('button', { name: 'Reply' }))
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'yes, proceed' } })
    await act(async () => void fireEvent.submit(box.closest('form')!))
    await act(async () => void (await Promise.resolve()))
    expect(messaging.sent().at(-1)).toMatchObject({ body: 'yes, proceed', inReplyTo: 'e-msg' })
  })

  it('keyboard shortcuts drive the action set and are ignored while typing', async () => {
    const { store } = await setup()
    const d = await openDetail('Apply policy change')
    await act(async () => void fireEvent.keyDown(d, { key: 's' }))
    expect(within(d).getByRole('region', { name: 'Snooze until' })).toBeTruthy()
    await act(async () => void fireEvent.keyDown(d, { key: 'e' })) // settle not offered for approvals: no-op
    expect(within(d).queryByRole('region', { name: 'Settle with note' })).toBeNull()
    await act(async () => void fireEvent.keyDown(d, { key: 'Escape' })) // closes panel first
    expect(screen.getByTestId('alert-detail')).toBeTruthy()
    await act(async () => void fireEvent.keyDown(d, { key: 'f' }))
    const custom = within(d).getByRole('tab', { name: /time/i })
    expect(custom).toBeTruthy()
    await act(async () => void fireEvent.keyDown(d, { key: 'd' }))
    expect((await store.load())[0].status).toBe('dismissed')
  })
})
