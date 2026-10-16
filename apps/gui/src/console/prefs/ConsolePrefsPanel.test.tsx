// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConsolePrefsPanel } from './ConsolePrefsPanel'
import { DEFAULT_CONSOLE_PREFS } from './types'

afterEach(cleanup)

describe('ConsolePrefsPanel', () => {
  it('emits patches for each control and states the critical rule', () => {
    const onChange = vi.fn()
    render(<ConsolePrefsPanel prefs={DEFAULT_CONSOLE_PREFS} onChange={onChange} />)
    expect(screen.getByText(/Critical alerts still break through/)).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Quiet mode'))
    expect(onChange).toHaveBeenLastCalledWith({ quiet: true })
    fireEvent.click(screen.getByLabelText('Quiet hours'))
    expect(onChange).toHaveBeenLastCalledWith({ quietHoursEnabled: true })
    expect((screen.getByLabelText('Quiet hours start') as HTMLInputElement).disabled).toBe(true)
    const muted = screen.getByPlaceholderText('agent-bravo, laptop')
    fireEvent.change(muted, { target: { value: 'a, b' } })
    expect(onChange).toHaveBeenCalledTimes(2)
    fireEvent.blur(muted)
    expect(onChange).toHaveBeenLastCalledWith({ mutedSources: ['a', ' b'] })
  })
})
