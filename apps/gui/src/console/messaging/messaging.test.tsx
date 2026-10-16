/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createActionBus, MockIdentityConfirm } from '../seams'
import { registerMessagingActions, messageAction } from './actions'
import { deliveryFromEnvelope, toEnvelopeRequest } from './adapter'
import { MessagingProvider } from './context'
import { createNudgeLimiter, validateSend } from './guards'
import { createMockMessagingClient, MOCK_RECIPIENTS } from './mock'
import { AgentMessageActions } from './QuickActions'
import { renderTemplate, NUDGE_TEMPLATES } from './templates'
import type { SendRequest } from './types'

afterEach(cleanup)
const [pm, , group] = MOCK_RECIPIENTS
const bus = () => createActionBus({ identity: new MockIdentityConfirm({ mode: 'auto-grant' }) })
const req = (o: Partial<SendRequest> = {}): SendRequest => ({ to: pm, intent: 'message', body: 'hi', ...o })

it('validates recipient, body and broadcast confirmation', () => {
  expect(validateSend(req())).toBeNull()
  expect(validateSend(req({ to: { ...pm, address: 'nope' } }))).toMatch(/recipient/)
  expect(validateSend(req({ body: '  ' }))).toMatch(/Write/)
  expect(validateSend(req({ to: group }))).toMatch(/Confirm the broadcast/)
  expect(validateSend(req({ to: group, confirmedBroadcast: true }))).toBeNull()
})

it('rate-limits nudges per agent through the action bus', async () => {
  let t = 1000
  const b = bus()
  const client = createMockMessagingClient({ now: () => t })
  registerMessagingActions(b, client, createNudgeLimiter({ windowMs: 60_000, now: () => t }))
  const nudge = messageAction(req({ intent: 'nudge', body: 'ping' }))
  expect((await b.dispatch(nudge)).status).toBe('ok')
  const second = await b.dispatch(nudge)
  expect(second.status).toBe('error')
  expect(second.message).toMatch(/Already nudged/)
  t += 61_000
  expect((await b.dispatch(nudge)).status).toBe('ok')
  expect(client.sent()).toHaveLength(2)
})

it('refuses an unconfirmed broadcast at the handler, not just the UI', async () => {
  const b = bus()
  const client = createMockMessagingClient()
  registerMessagingActions(b, client)
  expect((await b.dispatch(messageAction(req({ to: group })))).status).toBe('error')
  expect(client.sent()).toHaveLength(0)
})

it('progresses delivery state and supports replied', async () => {
  let t = 0
  const c = createMockMessagingClient({ now: () => t })
  const s = await c.send(req())
  expect(await c.getDelivery(s.id)).toBe('queued')
  t = 1000
  expect(await c.getDelivery(s.id)).toBe('delivered')
  t = 3000
  expect(await c.getDelivery(s.id)).toBe('read')
  c.markReplied(s.id)
  expect(await c.getDelivery(s.id)).toBe('replied')
})

it('maps to a Tether mail envelope and derives delivery', () => {
  const e = toEnvelopeRequest(req({ intent: 'update_request', expectedResponse: 'one line' }))
  expect(e.kind).toBe('request')
  expect(e.to).toBe(pm.address)
  expect(e.metadata?.['console.intent']).toBe('update_request')
  const base = { id: 'a', kind: 'notice' as const, from: 'x', to: 'y', created_at: '2026-01-01T00:00:00Z', delivered_at: null, consumed_at: null }
  expect(deliveryFromEnvelope(base)).toBe('queued')
  expect(deliveryFromEnvelope({ ...base, consumed_at: 'z' })).toBe('read')
  expect(deliveryFromEnvelope(base, [{ ...base, id: 'b', from: 'y', to: 'x', in_reply_to: 'a', created_at: '2026-01-01T00:01:00Z' }])).toBe('replied')
})

it('renders templates with and without a subject', () => {
  expect(renderTemplate(NUDGE_TEMPLATES[1], { subject: 'CW-1' })).toContain('blocked on CW-1')
  expect(renderTemplate(NUDGE_TEMPLATES[1])).toContain('blocked.')
})

it('sends a message from the dialog and shows delivery status', async () => {
  const client = createMockMessagingClient()
  render(<MessagingProvider client={client} actionBus={bus()}><AgentMessageActions recipient={pm} /></MessagingProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Message' }))
  const dlg = screen.getByRole('dialog', { name: 'Message' })
  expect((within(dlg).getByRole('button', { name: 'Send' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.change(within(dlg).getByLabelText('Message'), { target: { value: 'hello there' } })
  fireEvent.click(within(dlg).getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(within(dlg).getAllByText('QUEUED').length).toBeGreaterThan(0))
  expect(client.sent()[0].body).toBe('hello there')
})

it('nudge popover prefills a template and blocks a second nudge', async () => {
  const client = createMockMessagingClient()
  render(<MessagingProvider client={client} actionBus={bus()}><AgentMessageActions recipient={pm} subject="CW-9" /></MessagingProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Nudge' }))
  const pop = screen.getByRole('dialog', { name: 'Nudge' })
  fireEvent.click(within(pop).getByRole('button', { name: 'Blocked?' }))
  expect((within(pop).getByLabelText('Message') as HTMLTextAreaElement).value).toContain('blocked on CW-9')
  fireEvent.click(within(pop).getByRole('button', { name: 'Send nudge' }))
  await waitFor(() => expect(client.sent()).toHaveLength(1))
  fireEvent.click(within(pop).getByRole('button', { name: 'Send nudge' }))
  expect((await within(pop).findByRole('alert')).textContent).toMatch(/Already nudged/)
  expect(client.sent()).toHaveLength(1)
})

it('disables actions without a resolvable recipient', () => {
  render(<MessagingProvider client={createMockMessagingClient()} actionBus={bus()}><AgentMessageActions recipient={null} /></MessagingProvider>)
  for (const n of ['Message', 'Nudge', 'Request update']) expect((screen.getByRole('button', { name: n }) as HTMLButtonElement).disabled).toBe(true)
})
