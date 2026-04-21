/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { FilterCycleToggle } from './filter-cycle-toggle'

afterEach(() => {
  cleanup()
})

type V = 'both' | 'auto' | 'manual'

const OPTIONS: ReadonlyArray<{ value: V; label: string; dotColor?: string }> = [
  { value: 'both', label: 'Both', dotColor: 'bg-zinc-400' },
  { value: 'auto', label: 'Auto', dotColor: 'bg-blue-400' },
  { value: 'manual', label: 'Manual', dotColor: 'bg-amber-400' },
]

describe('FilterCycleToggle', () => {
  it('renders the label for the current value', () => {
    render(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={() => {}}
        ariaLabel="Manual filter"
      />,
    )
    expect(screen.getByRole('button', { name: /manual filter/i }).textContent).toContain('Auto')
  })

  it('advances to the next option on click and wraps at the end', () => {
    const onChange = vi.fn<(v: V) => void>()
    const { rerender } = render(
      <FilterCycleToggle
        options={OPTIONS}
        value="both"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('auto')

    rerender(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('manual')

    rerender(
      <FilterCycleToggle
        options={OPTIONS}
        value="manual"
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    expect(onChange).toHaveBeenLastCalledWith('both')
  })

  it('falls back to first option if current value is not in options', () => {
    const onChange = vi.fn<(v: V) => void>()
    render(
      <FilterCycleToggle
        options={OPTIONS}
        // Cast: simulates a corrupted state value.
        value={'bogus' as unknown as V}
        onChange={onChange}
        ariaLabel="Manual filter"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /manual filter/i }))
    // When current value isn't found, advancing from index 0 → 1 (next after "both").
    expect(onChange).toHaveBeenLastCalledWith('auto')
  })

  it('renders the dot with the active option color class', () => {
    const { container } = render(
      <FilterCycleToggle
        options={OPTIONS}
        value="auto"
        onChange={() => {}}
        ariaLabel="Manual filter"
      />,
    )
    const dot = container.querySelector('[data-testid="cycle-dot"]')
    expect(dot).not.toBeNull()
    expect(dot?.className).toContain('bg-blue-400')
  })
})
