// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { FixtureSource } from './fixture-source'
import { ConsoleProvider } from './provider'
import { useConsoleEvents, useConsoleState, useItemState } from './hooks'
import { KINDS } from './types'

function Probe() {
  const state = useConsoleState()
  const msgs = useConsoleEvents({ kinds: [KINDS.messageReceived] })
  const first = msgs[0]
  const item = useItemState(first?.threadId ?? first?.id ?? 'none')
  return (
    <div>
      <span data-testid="ready">{String(state.ready)}</span>
      <span data-testid="msgs">{msgs.length}</span>
      <button onClick={() => void item.dismiss()}>dismiss</button>
    </div>
  )
}

describe('ConsoleProvider', () => {
  it('opens exactly one subscription however many consumers mount, and filters handled items', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 16 })
    const sub = vi.spyOn(source, 'subscribe')
    await act(async () => {
      render(
        <ApiProvider client={{} as never}>
          <ConsoleProvider source={source} resyncMs={0}>
            <Probe />
            <Probe />
          </ConsoleProvider>
        </ApiProvider>,
      )
    })
    expect(sub).toHaveBeenCalledTimes(1)
    expect(screen.getAllByTestId('ready')[0].textContent).toBe('true')
    const before = Number(screen.getAllByTestId('msgs')[0].textContent)
    expect(before).toBeGreaterThan(0)
    await act(async () => {
      screen.getAllByText('dismiss')[0].click()
    })
    expect(Number(screen.getAllByTestId('msgs')[0].textContent)).toBeLessThan(before)
  })
})
