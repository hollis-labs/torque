// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { ConsoleProvider } from '../events/provider'
import { FixtureSource } from '../events/fixture-source'
import { MockIdentityConfirm } from '../seams/identity-confirm'
import { createActionBus } from '../seams/action-bus'
import type { ConsoleEvent } from '../events/types'
import { deriveCards } from './engine'
import { cardFixtureEvents } from './fixtures'
import { registerMockCardHandlers } from './mock-handlers'
import { DEFAULT_CARD_RULES, type CardRuleSet } from './rules'
import { CardInlineSlot, CardRuntimeProvider, CardTray, useCardHost } from './index'
import { renderTemplate } from './template'

afterEach(cleanup)
const T0 = 1_000_000_000_000
const ev = (o: Partial<ConsoleEvent> & { id: string; ts: number; kind: string }): ConsoleEvent => ({ source: { id: 't' }, subjects: [], severity: 'info', payload: {}, actions: [], extra: {}, ...o })

describe('rule engine', () => {
  it('coalesces same-key events into one live card with a count (latest wins)', () => {
    const cards = deriveCards(cardFixtureEvents(T0), DEFAULT_CARD_RULES, { now: T0 })
    const status = cards.filter((c) => c.kind === 'status')
    expect(status).toHaveLength(1)
    expect(status[0]).toMatchObject({ count: 2, summary: 'waiting', firstTs: T0 - 50_000, ts: T0 - 20_000 })
  })

  it('resolves a card when the resolver event arrives and reopens on a new round', () => {
    const req = (id: string, ts: number) => ev({ id, ts, kind: 'approval.requested', correlationId: 'a1', payload: { title: 'T' } })
    const res = ev({ id: 'r', ts: 20, kind: 'approval.resolved', correlationId: 'a1' })
    expect(deriveCards([req('q1', 10), res], DEFAULT_CARD_RULES)[0]).toMatchObject({ phase: 'resolved', resolution: { label: 'Resolved', eventId: 'r' } })
    expect(deriveCards([req('q1', 10), res, req('q2', 30)], DEFAULT_CARD_RULES)[0]).toMatchObject({ phase: 'live', count: 2 })
  })

  it('expires by payload expiry and by rule ttl', () => {
    const cards = deriveCards(cardFixtureEvents(T0), DEFAULT_CARD_RULES, { now: T0 + 4_000_000 })
    expect(cards.find((c) => c.kind === 'approval')?.phase).toBe('expired')
    const pr = ev({ id: 'p', ts: 0, kind: 'pr.opened', subjects: [{ type: 'pr', id: 'r#1' }], payload: { title: 'x' } })
    expect(deriveCards([pr], DEFAULT_CARD_RULES, { now: 25 * 3_600_000 })[0].phase).toBe('expired')
  })

  it('supersedes other cards named by the rule', () => {
    const set: CardRuleSet = {
      rules: [
        { id: 'a', match: { kinds: ['x.old'] }, card: { kind: 'info', surfaces: ['tray'], dedupeKey: 'old:{id}', title: 'old' } },
        { id: 'b', match: { kinds: ['x.new'] }, card: { kind: 'info', surfaces: ['tray'], dedupeKey: 'new:{id}', title: 'new', supersedes: ['old:{correlationId}'] } },
      ],
    }
    const cards = deriveCards([ev({ id: 'o1', ts: 1, kind: 'x.old' }), ev({ id: 'n1', ts: 2, kind: 'x.new', correlationId: 'o1' })], set)
    expect(cards.find((c) => c.id === 'old:o1')?.phase).toBe('superseded')
  })

  it('is data-driven: remapping kinds needs only a new rule set', () => {
    const remapped: CardRuleSet = { rules: [{ ...DEFAULT_CARD_RULES.rules[0], match: { kinds: ['hitl.ask'] } }] }
    const cards = deriveCards([ev({ id: 'h', ts: 1, kind: 'hitl.ask', correlationId: 'k', payload: { title: 'Ask' } })], remapped)
    expect(cards[0]).toMatchObject({ kind: 'approval', title: 'Ask', id: 'approval:k' })
    expect(cards[0].actions.map((a) => a.id)).toEqual(['approve', 'reject'])
    expect(cards[0].actions[0].confirm?.scope).toEqual(['approval:approve'])
  })

  it('event-offered actions win over declared defaults of the same id', () => {
    const card = deriveCards(cardFixtureEvents(T0), DEFAULT_CARD_RULES, { now: T0 }).find((c) => c.kind === 'approval')!
    expect(card.actions.find((a) => a.id === 'approve')).toMatchObject({ origin: 'offered', confirm: { scope: ['agent:apply'] } })
    expect(card.actions.find((a) => a.id === 'reject')).toMatchObject({ origin: 'offered', settlesAs: 'rejected', intent: 'danger' })
  })

  it('templates take the first non-empty alternative and never evaluate code', () => {
    const e = ev({ id: 'i', ts: 1, kind: 'k', payload: { a: '', b: '<b>x</b>', o: { z: 1 } } })
    expect(renderTemplate('{payload.a|payload.b}', e)).toBe('<b>x</b>')
    expect(renderTemplate('{payload.o}|{constructor}', e)).toBe('|')
  })
})

function App({ source, identity, children }: { source: FixtureSource; identity: MockIdentityConfirm; children: React.ReactNode }) {
  const actionBus = createActionBus({ identity })
  registerMockCardHandlers(actionBus)
  return (
    <ApiProvider client={{} as never}>
      <ConsoleProvider source={source} identity={identity} actionBus={actionBus} resyncMs={0}>
        <CardRuntimeProvider>{children}</CardRuntimeProvider>
      </ConsoleProvider>
    </ApiProvider>
  )
}

function seeded() {
  const source = new FixtureSource({ intervalMs: 0, initial: 0 })
  const now = Date.now()
  cardFixtureEvents(now).forEach((e) => source.emit(e))
  return source
}

describe('host surfaces', () => {
  it('tray lists live cards, expands a row, and approve runs step-up then settles the card', async () => {
    const identity = new MockIdentityConfirm({ mode: 'auto-grant' })
    await act(async () => { render(<App source={seeded()} identity={identity}><CardTray /></App>) })
    expect(await screen.findByText('Apply agent config change')).toBeTruthy()
    await act(async () => { screen.getByText('Apply agent config change').click() })
    await act(async () => { screen.getByText(/^Approve/).click() })
    await waitFor(() => expect(screen.queryByText('Apply agent config change')).toBeNull())
  })

  it('denied step-up keeps the card live and shows the error', async () => {
    const identity = new MockIdentityConfirm({ mode: 'auto-deny' })
    await act(async () => { render(<App source={seeded()} identity={identity}><CardTray /></App>) })
    await act(async () => { screen.getByText('Apply agent config change').click() })
    await act(async () => { screen.getByText(/^Approve/).click() })
    expect((await screen.findByRole('alert')).textContent).toMatch(/mock policy/)
    expect(screen.getByText('Apply agent config change')).toBeTruthy()
  })

  it('dismiss removes a card; inline slot filters by subject; modal opens via the host', async () => {
    const identity = new MockIdentityConfirm()
    function Open() {
      const host = useCardHost()
      return <button onClick={() => host.openModal('thread:thr-1')}>open</button>
    }
    await act(async () => {
      render(
        <App source={seeded()} identity={identity}>
          <CardInlineSlot subject={{ type: 'task', id: 'CW-1' }} density="expanded" />
          <Open />
        </App>,
      )
    })
    expect(await screen.findByText('Apply agent config change')).toBeTruthy()
    await act(async () => { screen.getByText('open').click() })
    expect(screen.getByRole('dialog').textContent).toMatch(/Need a decision/)
    await act(async () => { screen.getAllByText('Dismiss').at(-1)!.click() })
    await act(async () => { screen.getByText('Close').click() })
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
