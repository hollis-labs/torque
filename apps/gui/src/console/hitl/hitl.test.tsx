// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { CardTray, CardRuntimeProvider } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { deriveCards } from '../cards/engine'
import { DEFAULT_CARD_RULES } from '../cards/rules'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import type { ConsoleEvent } from '../events/types'
import { createActionBus, type ActionRecord } from '../seams/action-bus'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { HitlError, MockHitlClient, secretHandoff } from './client'
import { hitlFixtures } from './fixtures'
import { HitlProvider } from './provider'
import { registerHitlKinds } from './kinds'
import { fmtRemaining, HITL_CARD_RULES, parseHitlRequest, requestToCard, requestToEvent, withHitlRules } from './model'

afterEach(cleanup)
const T0 = 1_000_000_000_000
const fixtures = () => hitlFixtures(T0)
const byId = (id: string) => fixtures().find((r) => r.id === id)!
const granted = async (identity: MockIdentityConfirm) =>
  identity.confirm({ what: { action: 'a', title: 't' }, scope: ['hitl:approve'] })

describe('MockHitlClient', () => {
  it('refuses approve without a granted identity confirmation, accepts it with one, and audits the step-up', async () => {
    const c = new MockHitlClient(fixtures(), { now: () => T0 })
    await expect(c.respond('hitl-appr-1', { type: 'approval', decision: 'approve' }, {})).rejects.toMatchObject({ code: 'confirm_required' })
    const confirmation = await granted(new MockIdentityConfirm({ now: () => T0 }))
    const done = await c.respond('hitl-appr-1', { type: 'approval', decision: 'approve', reason: 'ok' }, { confirmation })
    expect(done.status).toBe('answered')
    expect(done.audit.map((a) => a.action)).toEqual(['requested', 'confirmed', 'approved'])
    expect(JSON.stringify(done)).not.toContain(confirmation.token!)
  })

  it('rejects expired and already-closed requests and mismatched responses', async () => {
    const c = new MockHitlClient(fixtures(), { now: () => T0 })
    await expect(c.respond('hitl-appr-exp', { type: 'approval', decision: 'reject' }, {})).rejects.toBeInstanceOf(HitlError)
    await expect(c.respond('hitl-appr-done', { type: 'approval', decision: 'reject' }, {})).rejects.toMatchObject({ code: 'not_pending' })
    await expect(c.respond('hitl-q-1', { type: 'form', values: {} }, {})).rejects.toMatchObject({ code: 'invalid' })
  })

  it('validates question options and required form fields', async () => {
    const c = new MockHitlClient(fixtures(), { now: () => T0 })
    await expect(c.respond('hitl-q-1', { type: 'question', answer: '' }, {})).rejects.toThrow()
    await expect(c.respond('hitl-form-1', { type: 'form', values: { repo: 'x' } }, {})).rejects.toThrow(/Required/)
    const r = await c.respond('hitl-form-1', { type: 'form', values: { repo: 'a/b', depth: 'quick', ack: true } }, {})
    expect(r.outcome?.label).toBe('Submitted')
  })

  it('never retains a secret value', async () => {
    const c = new MockHitlClient(fixtures(), { now: () => T0 })
    const r = await c.submitSecret('hitl-sec-1', 'ghp_SUPERSECRET', {})
    expect(c.secretsReceived).toBe(1)
    expect(JSON.stringify(r)).not.toContain('SUPERSECRET')
    expect(JSON.stringify(await c.list())).not.toContain('SUPERSECRET')
  })
})

describe('model', () => {
  it('maps requests to cards with phase from status and local expiry', () => {
    expect(requestToCard(byId('hitl-appr-1'), T0)).toMatchObject({ id: 'hitl:hitl-appr-1', kind: 'hitl.approval', phase: 'live', severity: 'warning' })
    expect(requestToCard(byId('hitl-appr-1'), T0).actions.map((a) => a.id)).toEqual(['approve', 'reject', 'defer'])
    expect(requestToCard(byId('hitl-appr-1'), T0).actions[0].confirm?.scope).toContain('hitl:approve')
    expect(requestToCard(byId('hitl-appr-exp'), T0).phase).toBe('expired')
    expect(requestToCard(byId('hitl-appr-done'), T0)).toMatchObject({ phase: 'resolved', resolution: { label: 'Approved' } })
    expect(requestToCard(byId('hitl-sec-1'), T0).surfaces).not.toContain('inline')
  })

  it('parses untrusted payloads tolerantly', () => {
    expect(parseHitlRequest({ id: 'x', type: 'bogus' })).toBeUndefined()
    expect(parseHitlRequest('nope')).toBeUndefined()
    expect(parseHitlRequest({ id: 'x', type: 'approval', title: 5, risk: 'weird' })).toMatchObject({ title: '(untitled request)', risk: 'medium', status: 'pending', scope: [] })
  })

  it('derives the same cards from events via swappable rules, and resolvers close them', () => {
    const req = byId('hitl-q-1')
    const ev = requestToEvent(req)
    const set = withHitlRules(DEFAULT_CARD_RULES)
    expect(deriveCards([ev], set, { now: T0 })[0]).toMatchObject({ kind: 'hitl.question', id: 'hitl:hitl-q-1' })
    const resolved: ConsoleEvent = { ...ev, id: 'r', ts: ev.ts + 1, kind: 'hitl.resolved', payload: { id: req.id } }
    expect(deriveCards([ev, resolved], set, { now: T0 })[0].phase).toBe('resolved')
    expect(HITL_CARD_RULES.rules).toHaveLength(4)
  })

  it('formats the countdown', () => {
    expect(fmtRemaining(0)).toBe('expired')
    expect(fmtRemaining(42_000)).toBe('42s')
    expect(fmtRemaining(4 * 60_000 + 12_000)).toBe('4m 12s')
    expect(fmtRemaining(2 * 3_600_000 + 5 * 60_000)).toBe('2h 05m')
  })
})

function setup(mode: 'auto-grant' | 'auto-deny' = 'auto-grant', now = Date.now()) {
  const identity = new MockIdentityConfirm({ mode })
  const bus = createActionBus({ identity })
  const records: ActionRecord[] = []
  bus.subscribe((r) => records.push(r))
  const registry = createDefaultCardRegistry()
  registerHitlKinds(registry)
  const client = new MockHitlClient(hitlFixtures(now))
  const ui = (
    <ApiProvider client={{} as never}>
      <ConsoleProvider source={new FixtureSource({ intervalMs: 0, initial: 0 })} identity={identity} actionBus={bus} resyncMs={0}>
        <CardRuntimeProvider registry={registry} mountSurfaces={false}>
          <HitlProvider client={client}>
            <CardTray includeClosed />
          </HitlProvider>
        </CardRuntimeProvider>
      </ConsoleProvider>
    </ApiProvider>
  )
  return { ui, client, records }
}

describe('HITL cards', () => {
  it('approve: step-up then the card shows Approved and the audit has the confirmation', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('Apply agent config change')).click() })
    const card = within(screen.getByLabelText('Approval: Apply agent config change'))
    expect(card.getByText(/Token budget 200k/)).toBeTruthy()
    expect(card.getByText(/expires in/)).toBeTruthy()
    await act(async () => { card.getByText(/^Approve/).click() })
    await waitFor(async () => expect((await client.get('hitl-appr-1'))?.status).toBe('answered'))
    expect((await client.get('hitl-appr-1'))!.audit.map((a) => a.action)).toContain('confirmed')
  })

  it('denied step-up never reaches the client and shows the error', async () => {
    const { ui, client } = setup('auto-deny')
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('Apply agent config change')).click() })
    await act(async () => { within(screen.getByLabelText('Approval: Apply agent config change')).getByText(/^Approve/).click() })
    expect((await screen.findByRole('alert')).textContent).toMatch(/mock policy/)
    expect((await client.get('hitl-appr-1'))?.status).toBe('pending')
  })

  it('expired approval is locked: no decision buttons', async () => {
    const { ui } = setup()
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('Force-push release branch')).click() })
    expect(screen.getByText(/Expired - ask the requester/)).toBeTruthy()
  })

  it('question: choose an option and send', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('Which migration strategy?')).click() })
    const send = screen.getByText('Send answer') as HTMLButtonElement
    expect(send.disabled).toBe(true)
    await act(async () => { fireEvent.click(screen.getByLabelText(/Expand then contract/)) })
    await act(async () => { send.click() })
    await waitFor(async () => expect((await client.get('hitl-q-1'))?.outcome?.label).toBe('Answered'))
  })

  it('form: required fields block submit with inline errors, then submit succeeds', async () => {
    const { ui, client } = setup()
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('Launch parameters')).click() })
    await act(async () => { screen.getByText('Launch').click() })
    expect(screen.getAllByRole('alert').length).toBeGreaterThanOrEqual(3)
    await act(async () => {
      fireEvent.change(screen.getByLabelText(/Repository/), { target: { value: 'hollis-labs/torque' } })
      fireEvent.change(screen.getByLabelText(/Review depth/), { target: { value: 'deep' } })
      fireEvent.click(screen.getByLabelText(/review policy/))
    })
    await act(async () => { screen.getByText('Launch').click() })
    await waitFor(async () => expect((await client.get('hitl-form-1'))?.status).toBe('answered'))
  })

  it('secret: value is delivered once and never appears in action records, DOM or the handoff', async () => {
    const { ui, client, records } = setup()
    await act(async () => { render(ui) })
    await act(async () => { (await screen.findByText('GitHub token needed')).click() })
    const input = screen.getByLabelText('Personal access token') as HTMLInputElement
    expect(input.type).toBe('password')
    await act(async () => { fireEvent.change(input, { target: { value: 'ghp_SUPERSECRET' } }) })
    await act(async () => { screen.getByText('Send secret').click() })
    await waitFor(async () => expect((await client.get('hitl-sec-1'))?.status).toBe('answered'))
    expect(client.secretsReceived).toBe(1)
    expect(JSON.stringify(records)).not.toContain('SUPERSECRET')
    expect(document.body.innerHTML).not.toContain('SUPERSECRET')
    expect(secretHandoff.size).toBe(0)
  })
})
