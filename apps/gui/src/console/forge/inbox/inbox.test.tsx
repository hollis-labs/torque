// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'
import { ConsoleProvider } from '../../events/provider'
import { FixtureSource } from '../../events/fixture-source'
import { MessagingProvider } from '../../messaging/context'
import { createMockMessagingClient } from '../../messaging/mock'
import { createActionBus } from '../../seams/action-bus'
import { MockIdentityConfirm } from '../../seams/identity-confirm'
import { MemoryStateStore } from '../../seams/state-store'
import { createMockForgeClient } from '../mock'
import type { PendingPrSummary } from '../types'
import { assignAction, registerInboxActions } from './actions'
import { buildAssignmentPayload, parseAssignmentBody, renderAssignmentBody, suggestTask } from './assign'
import { prWorkspaceHref } from './href'
import { buildItems, categoriesOf, DEFAULT_FILTER, filterItems, groupItems, triageKey } from './model'
import { PendingPrInbox } from './PendingPrInbox'
import { defaultPendingPrEventMapper, PendingPrSource, pendingPrAlertMapper } from './source'

afterEach(cleanup)
const NOW = Date.parse('2026-10-16T12:00:00Z')
const repo = { host: 'github', owner: 'o', name: 'r' }
const mk = (n: number, over: Partial<PendingPrSummary> = {}): PendingPrSummary => ({
  ref: { repo, number: n },
  title: `PR ${n}`,
  author: { login: 'dev' },
  assignees: [],
  state: 'open',
  reasons: ['review_requested'],
  mergeable: 'mergeable',
  updatedAt: '2026-10-16T10:00:00Z',
  ...over,
})

describe('model', () => {
  it('categorises by reason and by staleness', () => {
    expect(categoriesOf(mk(1, { reasons: ['checks_failed', 'review_requested'] }), NOW)).toEqual(['failing_checks', 'needs_review'])
    expect(categoriesOf(mk(2, { updatedAt: '2026-10-01T00:00:00Z' }), NOW)).toEqual(['needs_review', 'stale'])
  })

  it('groups by category with one home per PR, and by repo', () => {
    const prs = [mk(1, { reasons: ['checks_failed', 'review_requested'] }), mk(2), mk(3, { ref: { repo: { ...repo, name: 'z' }, number: 3 } })]
    const items = buildItems(prs, {}, NOW)
    expect(groupItems(items, 'category').map((g) => [g.id, g.items.length])).toEqual([['failing_checks', 1], ['needs_review', 2]])
    expect(groupItems(items, 'repo').map((g) => g.label)).toEqual(['o/r', 'o/z'])
  })

  it('applies triage state to the views and filters', () => {
    const prs = [mk(1), mk(2), mk(3), mk(4)]
    const state = {
      [triageKey(prs[0])]: { key: triageKey(prs[0]), status: 'dismissed' as const, updatedAt: 1 },
      [triageKey(prs[1])]: { key: triageKey(prs[1]), status: 'snoozed' as const, snoozeUntil: NOW + 1000, updatedAt: 1 },
      [triageKey(prs[2])]: { key: triageKey(prs[2]), status: 'snoozed' as const, snoozeUntil: NOW - 1000, updatedAt: 1 },
    }
    const items = buildItems(prs, state, NOW)
    expect(filterItems(items, DEFAULT_FILTER).map((i) => i.pr.ref.number).sort()).toEqual([3, 4])
    expect(filterItems(items, { ...DEFAULT_FILTER, view: 'snoozed' }).map((i) => i.pr.ref.number)).toEqual([2])
    expect(filterItems(items, { ...DEFAULT_FILTER, search: 'pr 4' }).map((i) => i.pr.ref.number)).toEqual([4])
  })
})

describe('assignment payload', () => {
  it('round-trips through the message body and picks a task from reasons', () => {
    const pr = mk(7, { reasons: ['checks_failed'], checksSummary: { passed: 1, failed: 2, pending: 0 } })
    expect(suggestTask(pr)).toBe('fix_checks')
    const payload = buildAssignmentPayload(pr, 'fix_checks', 'agent-x', { note: ' be careful ' })
    const body = renderAssignmentBody(payload)
    expect(body).toContain('o/r#7')
    expect(body).toContain('Checks: 1 passed, 2 failed')
    expect(parseAssignmentBody(body)).toEqual(payload)
    expect(payload.note).toBe('be careful')
    expect(payload.pr.workspace).toBe(prWorkspaceHref(pr.ref))
  })
})

describe('assign action', () => {
  const setup = (mode: 'auto-grant' | 'auto-deny' = 'auto-grant') => {
    const identity = new MockIdentityConfirm({ mode })
    const bus = createActionBus({ identity })
    const forge = createMockForgeClient()
    const messaging = createMockMessagingClient()
    const stateStore = new MemoryStateStore()
    registerInboxActions(bus, { forge, messaging, stateStore })
    return { bus, forge, messaging, stateStore }
  }
  const agent = { id: 'team-worker', name: 'Team Worker', urn: 'msg://agent/agent-mux/agt_tw02' }

  it('sets the forge assignee, sends the structured message, records it, and de-dupes', async () => {
    const { bus, forge, messaging, stateStore } = setup()
    const [pr] = (await forge.listPendingPrs()).filter((p) => p.assignees.length === 0)
    const res = await bus.dispatch(assignAction({ pr, agent, task: 'review' }))
    expect(res.status).toBe('ok')
    expect(res.confirmation?.scope).toEqual(['forge:assign', 'message:send'])
    expect((await forge.getPr(pr.ref)).assignees.map((a) => a.login)).toContain('team-worker')
    const [msg] = messaging.sent()
    expect(msg.to).toBe(agent.urn)
    expect(parseAssignmentBody(msg.body)?.pr.number).toBe(pr.ref.number)
    expect((await stateStore.load()).some((s) => s.resolution === 'assigned')).toBe(true)

    const again = await bus.dispatch(assignAction({ pr, agent, task: 'review' }))
    expect(again.status).toBe('error')
    expect(again.message).toMatch(/Already assigned/)
    expect(messaging.sent()).toHaveLength(1)
    expect((await bus.dispatch(assignAction({ pr, agent, task: 'review', force: true }))).status).toBe('ok')
    expect(messaging.sent()).toHaveLength(2)
  })

  it('does nothing when the step-up is denied', async () => {
    const { bus, forge, messaging, stateStore } = setup('auto-deny')
    const [pr] = await forge.listPendingPrs()
    const res = await bus.dispatch(assignAction({ pr, agent, task: 'review' }))
    expect(res.status).toBe('denied')
    expect(messaging.sent()).toHaveLength(0)
    expect(await stateStore.load()).toEqual([])
  })

  it('reports a failed message and records nothing', async () => {
    const identity = new MockIdentityConfirm()
    const bus = createActionBus({ identity })
    const forge = createMockForgeClient()
    const stateStore = new MemoryStateStore()
    registerInboxActions(bus, { forge, messaging: createMockMessagingClient({ failFor: [agent.urn] }), stateStore })
    const [pr] = await forge.listPendingPrs()
    const res = await bus.dispatch(assignAction({ pr, agent, task: 'review', setForgeAssignee: false }))
    expect(res.status).toBe('error')
    expect(await stateStore.load()).toEqual([])
  })
})

describe('detect and surface', () => {
  it('maps one event per PR to its most urgent reason, with stable ids', () => {
    const pr = mk(5, { reasons: ['checks_failed', 'review_requested'] })
    const [e] = defaultPendingPrEventMapper(pr, { now: NOW, staleMs: 7 * 86_400_000 })
    expect(e.kind).toBe('pr.checks_failed')
    expect(e.subjects[0]).toMatchObject({ type: 'pr', id: 'github:o/r#5' })
    expect(e.actions[0].params?.href).toBe(prWorkspaceHref(pr.ref))
    expect(defaultPendingPrEventMapper(pr, { now: NOW, staleMs: 1 })[0].id).toBe(e.id)
    expect(defaultPendingPrEventMapper(mk(6, { reasons: ['unassigned'] }), { now: NOW, staleMs: 7 * 86_400_000 })).toEqual([])
    const alert = pendingPrAlertMapper()(defaultPendingPrEventMapper(mk(8, { reasons: ['changes_requested'] }), { now: NOW, staleMs: 1e12 })[0])
    expect(alert?.title).toBe('o/r#8 changes requested')
  })

  it('source emits only new events when a PR changes', async () => {
    const forge = createMockForgeClient()
    const src = new PendingPrSource(forge, { pollMs: 5, now: () => NOW })
    const snap = await src.snapshot()
    expect(snap.events.length).toBeGreaterThan(0)
    const got: string[] = []
    const off = src.subscribe({ onEvents: (e) => got.push(...e.map((x) => x.id)), onStatus: () => undefined, onGap: () => undefined })
    await new Promise((r) => setTimeout(r, 30))
    off()
    expect(got).toEqual([])
  })
})

describe('<PendingPrInbox>', () => {
  const mount = () => {
    const identity = new MockIdentityConfirm()
    const bus = createActionBus({ identity })
    const forge = createMockForgeClient()
    const messaging = createMockMessagingClient()
    const stateStore = new MemoryStateStore()
    render(
      <MemoryRouter>
        <ConsoleProvider source={new FixtureSource({ intervalMs: 0, initial: 0 })} identity={identity} actionBus={bus} stateStore={stateStore} resyncMs={0}>
          <MessagingProvider client={messaging} actionBus={bus}>
            <PendingPrInbox forge={forge} pollMs={0} />
          </MessagingProvider>
        </ConsoleProvider>
      </MemoryRouter>,
    )
    return { messaging, stateStore, forge }
  }

  it('lists grouped PRs, links to the workspace, and triages via the StateStore', async () => {
    const { stateStore, forge } = mount()
    const pending = await forge.listPendingPrs()
    await screen.findByText(new RegExp(`#${pending[0].ref.number}$`))
    const link = screen.getAllByRole('link')[0]
    expect(link.getAttribute('href')).toMatch(/^\/pr\/github\//)
    const rows = () => screen.getAllByTestId(/^pr-row-/)
    const before = rows().length
    fireEvent.click(within(rows()[0]).getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(rows().length).toBe(before - 1))
    expect((await stateStore.load())[0].status).toBe('dismissed')
    fireEvent.click(screen.getByRole('tab', { name: /Dismissed/ }))
    await waitFor(() => expect(rows()).toHaveLength(1))
    fireEvent.click(within(rows()[0]).getByRole('button', { name: 'Reopen' }))
    await waitFor(() => expect(screen.getByText('No pull requests match this view.')).toBeTruthy())
  })

  it('assigns through the picker, confirms, and shows the assignment on the row', async () => {
    const { messaging } = mount()
    await screen.findAllByTestId(/^pr-row-/)
    fireEvent.click(screen.getAllByRole('button', { name: 'Assign' })[0])
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByText('Team Worker')
    const submit = within(dialog).getByRole('button', { name: 'Assign' })
    expect((submit as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(within(dialog).getByRole('radio', { name: /Team Worker/ }))
    fireEvent.click(submit)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(messaging.sent()).toHaveLength(1)
    expect(await screen.findByText(/Team Worker: .* · sent/)).toBeTruthy()
  })
})
