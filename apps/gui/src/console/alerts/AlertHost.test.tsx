// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { FixtureSource } from '../events/fixture-source'
import { ConsoleProvider } from '../events/provider'
import { AlertHost } from './AlertHost'
import { AlertToast } from './AlertToast'
import { AlertTrayTrigger } from './AlertTrayTrigger'
import { ConsoleShell } from './ConsoleShell'
import { MemoryAlertPrefsStore } from './prefs'
import { sampleAlerts } from './samples'

afterEach(cleanup)

async function setup(prefs = new MemoryAlertPrefsStore()) {
  const source = new FixtureSource({ intervalMs: 0, initial: 16 })
  const sub = vi.spyOn(source, 'subscribe')
  await act(async () => {
    render(
      <MemoryRouter>
        <ApiProvider client={{} as never}>
          <ConsoleProvider source={source} resyncMs={0}>
            <AlertHost prefsStore={prefs}>
              <AlertTrayTrigger />
            </AlertHost>
          </ConsoleProvider>
        </ApiProvider>
      </MemoryRouter>,
    )
  })
  return { source, sub, prefs }
}

// Fixture event 17 is n%8===1 (agent.state, mostly dropped); 19 is a message (notice), 20 an approval/other.
async function emitUntilToast(source: FixtureSource, max = 8) {
  for (let i = 0; i < max && screen.queryAllByTestId('alert-toast').length === 0; i++) await act(async () => source.tick())
}

describe('AlertHost', () => {
  it('opens no stream of its own, skips the backlog, then toasts new alerts', async () => {
    const { source, sub } = await setup()
    expect(sub).toHaveBeenCalledTimes(1)
    expect(screen.queryAllByTestId('alert-toast')).toHaveLength(0)
    await emitUntilToast(source)
    expect(screen.getAllByTestId('alert-toast').length).toBeGreaterThan(0)
  })

  it('quiet mode suppresses toasts but the badge still counts', async () => {
    const { source } = await setup(new MemoryAlertPrefsStore({ quiet: true }))
    for (let i = 0; i < 8; i++) await act(async () => source.tick())
    expect(screen.queryAllByTestId('alert-toast')).toHaveLength(0)
    expect(screen.getByTestId('alert-badge')).toBeTruthy()
  })

  it('tray lists alerts, dismiss moves to history, restore brings it back, Escape closes', async () => {
    await setup()
    const trigger = screen.getByRole('button', { name: /^Alerts/ })
    await act(async () => fireEvent.click(trigger))
    const dialog = screen.getByRole('dialog', { name: 'Alerts' })
    const before = within(dialog).getAllByTestId('alert-item').length
    expect(before).toBeGreaterThan(0)
    await act(async () => fireEvent.click(within(dialog).getAllByRole('button', { name: /^Dismiss:/ })[0]))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(before - 1)
    await act(async () => fireEvent.click(within(dialog).getByRole('tab', { name: /history/i })))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(1)
    await act(async () => fireEvent.click(within(dialog).getByRole('button', { name: /^Restore:/ })))
    await act(async () => fireEvent.click(within(dialog).getByRole('tab', { name: /active/i })))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(before)
    await act(async () => fireEvent.keyDown(dialog, { key: 'Escape' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('mark all read clears the badge', async () => {
    await setup()
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Mark all read' })))
    expect(screen.queryByTestId('alert-badge')).toBeNull()
  })
})

describe('AlertToast', () => {
  it('auto-expires per severity, pauses on hover, and stays for critical', () => {
    vi.useFakeTimers()
    try {
      const onClose = vi.fn()
      const [critical, , , notice] = sampleAlerts(Date.now())
      const { rerender } = render(
        <MemoryRouter>
          <AlertToast alert={notice} now={Date.now()} onClose={onClose} />
        </MemoryRouter>,
      )
      const el = screen.getByTestId('alert-toast')
      fireEvent.mouseEnter(el)
      act(() => void vi.advanceTimersByTime(60_000))
      expect(onClose).not.toHaveBeenCalled()
      fireEvent.mouseLeave(el)
      act(() => void vi.advanceTimersByTime(7_100))
      expect(onClose).toHaveBeenCalledTimes(1)
      onClose.mockClear()
      rerender(
        <MemoryRouter>
          <AlertToast alert={critical} now={Date.now()} onClose={onClose} />
        </MemoryRouter>,
      )
      act(() => void vi.advanceTimersByTime(120_000))
      expect(onClose).not.toHaveBeenCalled()
      fireEvent.keyDown(screen.getByTestId('alert-toast'), { key: 'Escape' })
      expect(onClose).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('ConsoleShell', () => {
  it('renders children untouched when not enabled', () => {
    render(
      <ConsoleShell>
        <AlertTrayTrigger />
        <span>child</span>
      </ConsoleShell>,
    )
    expect(screen.getByText('child')).toBeTruthy()
    expect(screen.queryByRole('button')).toBeNull()
  })
})
