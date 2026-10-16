// @vitest-environment jsdom
import { memo } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { MailBadge } from '@/components/planner/fleet-parts'
import { FleetAgentsView } from '@/components/planner/fleet-agents'
import { matchFocusedAgent, type FleetAgent } from '@/lib/fleet-agents'
import { AlertTrayTrigger } from '../alerts/AlertTrayTrigger'
import { ConsoleShell } from '../alerts/ConsoleShell'
import { AgentUnreadProvider } from '../messaging/unread'
import { createUnreadStore, fleetAgentAddress } from '../messaging/unread-store'
import { ConsoleProvider } from '../events/provider'
import { FixtureSource } from '../events/fixture-source'
import { AlertHost } from '../alerts/AlertHost'
import { MemoryAlertPrefsStore } from '../alerts/prefs'
import { MessagingProvider } from '../messaging/context'
import { createMockMessagingClient } from '../messaging/mock'
import { ConsoleNavExtras, useConsoleEnabled } from '.'

afterEach(() => {
  cleanup()
  window.history.replaceState(null, '', '/')
  vi.useRealTimers()
})

function Probe() {
  return <span data-testid="probe">{String(useConsoleEnabled())}</span>
}

function shell() {
  return (
    <MemoryRouter>
      <ApiProvider client={{} as never}>
        <ConsoleShell>
          <Probe />
          <ConsoleNavExtras alerts={<AlertTrayTrigger />} />
        </ConsoleShell>
      </ApiProvider>
    </MemoryRouter>
  )
}

describe('focus return', () => {
  it('alert tray: Escape closes it and focus goes back to the bell; chat dock likewise', async () => {
    window.history.replaceState(null, '', '/?console=fixture')
    await act(async () => void render(shell()))
    const bell = screen.getByLabelText(/^Alerts/)
    bell.focus()
    await act(async () => void fireEvent.click(bell))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    expect(document.activeElement).toBe(tray)
    await act(async () => void fireEvent.keyDown(tray, { key: 'Escape' }))
    expect(screen.queryByRole('dialog', { name: 'Alerts' })).toBeNull()
    expect(document.activeElement).toBe(screen.getByLabelText(/^Alerts/))

    const chat = screen.getByLabelText(/^Chat/)
    chat.focus()
    await act(async () => void fireEvent.click(chat))
    expect(document.activeElement).toBe(screen.getByLabelText('Chat drawer'))
    await act(async () => void fireEvent.keyDown(window, { key: 'Escape' }))
    expect(document.activeElement).toBe(screen.getByLabelText(/^Chat/))
  })
})

describe('ConsoleShell wiring', () => {
  it('is inert without ?console=: no triggers, flag false', async () => {
    await act(async () => void render(shell()))
    expect(screen.getByTestId('probe').textContent).toBe('false')
    expect(screen.queryByLabelText(/^Chat/)).toBeNull()
    expect(screen.queryByLabelText(/^Alerts/)).toBeNull()
  })

  it('mounts the chat dock trigger and opens the drawer in fixture mode; Escape closes it', async () => {
    window.history.replaceState(null, '', '/?console=fixture')
    await act(async () => void render(shell()))
    expect(screen.getByTestId('probe').textContent).toBe('true')
    await act(async () => void fireEvent.click(screen.getByLabelText(/^Chat/)))
    expect(screen.getByLabelText('Chat drawer')).toBeTruthy()
    await act(async () => void fireEvent.keyDown(window, { key: 'Escape' }))
    expect(screen.queryByLabelText('Chat drawer')).toBeNull()
  })
})

const agent = (over: Partial<FleetAgent> = {}): FleetAgent => ({
  id: 'p123', provider: 'claude', pid: 1, sessionId: null, cwd: '/home/u/dev/torque', state: 'idle', waitingFor: null, startedAt: null, lastActivity: null,
  stateConfidence: null, confidence: {}, model: null, lastAction: null, workstream: null, messages: null, ...over,
})

describe('unread fallback for MailBadge', () => {
  it('batches one unreadCounts call for all badges and shows the count when the feed has none', async () => {
    const client = createMockMessagingClient({ deliveredAfterMs: 1e9, readAfterMs: 1e9 })
    const a = agent(), b = agent({ id: 'p456' })
    await client.send({ to: { address: fleetAgentAddress(a), label: 'a', kind: 'agent' }, intent: 'message', body: 'hi' })
    const spy = vi.spyOn(client, 'unreadCounts')
    await act(async () => {
      render(
        <MessagingProvider client={client} actionBus={undefined}>
          <AgentUnreadProvider>
            <MailBadge count={null} agent={a} />
            <MailBadge count={null} agent={b} />
            <MailBadge count={2} agent={b} />
          </AgentUnreadProvider>
        </MessagingProvider>,
      )
    })
    await act(async () => void (await new Promise((r) => setTimeout(r, 120))))
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy.mock.calls[0][0].sort()).toEqual([fleetAgentAddress(a), fleetAgentAddress(b)].sort())
    expect(screen.getByLabelText('1 unread messages')).toBeTruthy()
    expect(screen.getByLabelText('2 unread messages')).toBeTruthy()
  })

  it('stays "not reported" and polls nothing outside a provider', () => {
    render(<MailBadge count={null} agent={agent()} />)
    expect(screen.getByTitle('Messages not reported')).toBeTruthy()
  })

  it('does not poll once every badge unmounts, and tolerates adapters without unreadCounts', async () => {
    vi.useFakeTimers()
    const client = createMockMessagingClient()
    const spy = vi.spyOn(client, 'unreadCounts')
    const store = createUnreadStore(client, { pollMs: 1000, settleMs: 10 })
    const off = store.watch('msg://agent/x/y')
    await vi.advanceTimersByTimeAsync(2500)
    const n = spy.mock.calls.length
    expect(n).toBeGreaterThanOrEqual(2)
    off()
    await vi.advanceTimersByTimeAsync(5000)
    expect(spy.mock.calls.length).toBe(n)
    const bare = createUnreadStore({ ...client, unreadCounts: undefined })
    bare.watch('a')()
    expect(bare.get('a')).toBeNull()
  })
})

describe('?agent= deep link', () => {
  it('matches by id, then provider, then folder name', () => {
    const list = [agent({ id: 'a1', provider: 'codex', cwd: '/x/alpha' }), agent({ id: 'b2', provider: 'claude', cwd: '/x/beta' })]
    expect(matchFocusedAgent(list, 'b2')?.id).toBe('b2')
    expect(matchFocusedAgent(list, 'Claude')?.id).toBe('b2')
    expect(matchFocusedAgent(list, 'alpha')?.id).toBe('a1')
    expect(matchFocusedAgent(list, 'nobody')).toBeNull()
  })

  it('opens the focused agent modal once agents load and reports close', async () => {
    const onClose = vi.fn()
    const view = (agents: FleetAgent[] | null) => (
      <MemoryRouter>
        <FleetAgentsView agents={agents} history={{}} error={null} now={Date.now()} live={false} focus="p123" onFocusClose={onClose} />
      </MemoryRouter>
    )
    const { rerender } = render(view(null))
    expect(screen.queryByRole('dialog')).toBeNull()
    rerender(view([agent()]))
    expect(screen.getByRole('dialog')).toBeTruthy()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('event storm (render stability)', () => {
  it('300 fixture events do not re-render the app subtree, and open no extra subscription', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 45 })
    const subscribe = vi.spyOn(source, 'subscribe')
    let appRenders = 0
    const App = memo(function App() {
      appRenders++
      return <ConsoleNavExtras alerts={<AlertTrayTrigger />} />
    })
    const tree = (
      <MemoryRouter>
        <ApiProvider client={{} as never}>
          <ConsoleProvider source={source} resyncMs={0}>
            <MessagingProvider client={createMockMessagingClient()}>
              <AgentUnreadProvider>
                <AlertHost prefsStore={new MemoryAlertPrefsStore()}>
                  <App />
                </AlertHost>
              </AgentUnreadProvider>
            </MessagingProvider>
          </ConsoleProvider>
        </ApiProvider>
      </MemoryRouter>
    )
    await act(async () => void render(tree))
    const before = appRenders
    const t0 = performance.now()
    for (let i = 0; i < 300; i++) await act(async () => void source.tick())
    expect(appRenders - before).toBe(0)
    expect(subscribe).toHaveBeenCalledTimes(1)
    expect(performance.now() - t0).toBeLessThan(15_000)
  })
})
