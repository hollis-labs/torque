/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react'
import { FilterSearchInput } from './filter-search-input'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('FilterSearchInput', () => {
  it('debounces onChange by 250ms', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="" onChange={onChange} />)
    const input = screen.getByRole('searchbox')
    fireEvent.change(input, { target: { value: 's' } })
    fireEvent.change(input, { target: { value: 'sc' } })
    fireEvent.change(input, { target: { value: 'sch' } })

    // Not yet — still under the debounce window.
    expect(onChange).not.toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenLastCalledWith('sch')
  })

  it('does not emit when typing matches the current value (no spurious fires)', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="foo" onChange={onChange} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    expect(input.value).toBe('foo')

    // No local changes, no emission.
    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('Esc clears the input and blurs', () => {
    const onChange = vi.fn<(v: string) => void>()
    render(<FilterSearchInput value="foo" onChange={onChange} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    input.focus()
    expect(document.activeElement).toBe(input)

    fireEvent.keyDown(input, { key: 'Escape' })
    expect(input.value).toBe('')
    expect(document.activeElement).not.toBe(input)

    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(onChange).toHaveBeenLastCalledWith('')
  })

  it('/ key focuses the input when no other input is focused', () => {
    render(<FilterSearchInput value="" onChange={() => {}} />)
    const input = screen.getByRole('searchbox')
    expect(document.activeElement).not.toBe(input)

    fireEvent.keyDown(window, { key: '/' })
    expect(document.activeElement).toBe(input)
  })

  it('/ key is a no-op when another input is focused', () => {
    render(
      <>
        <input data-testid="other" />
        <FilterSearchInput value="" onChange={() => {}} />
      </>,
    )
    const other = screen.getByTestId('other')
    const search = screen.getByRole('searchbox')
    other.focus()
    expect(document.activeElement).toBe(other)

    fireEvent.keyDown(window, { key: '/' })
    // Other input stays focused; search did not steal.
    expect(document.activeElement).toBe(other)
    expect(document.activeElement).not.toBe(search)
  })

  it('external value changes update the displayed text', () => {
    const { rerender } = render(<FilterSearchInput value="old" onChange={() => {}} />)
    const input = screen.getByRole('searchbox') as HTMLInputElement
    expect(input.value).toBe('old')

    rerender(<FilterSearchInput value="new" onChange={() => {}} />)
    expect(input.value).toBe('new')
  })
})
