/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { FilterEntityCombobox } from './filter-entity-combobox'

// jsdom polyfills for cmdk (Command) which touches ResizeObserver + scrollIntoView.
if (typeof globalThis.ResizeObserver === 'undefined') {
  class ResizeObserverPolyfill {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  ;(globalThis as unknown as { ResizeObserver: typeof ResizeObserverPolyfill }).ResizeObserver =
    ResizeObserverPolyfill
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function scrollIntoView() {}
}

afterEach(() => {
  cleanup()
})

const ITEMS = [
  { id: 'prj_a', name: 'Alpha' },
  { id: 'prj_b', name: 'Beta' },
  { id: 'prj_c', name: 'Charlie' },
]

describe('FilterEntityCombobox', () => {
  it('renders the muted "all" label when value is null', () => {
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    expect(screen.getByRole('button', { name: /filter by project/i }).textContent).toContain('All projects')
  })

  it('renders the selected item name when value is set', () => {
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value="prj_b"
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    expect(screen.getByRole('button', { name: /filter by project/i }).textContent).toContain('Beta')
  })

  it('opens a popover with all items on click and selects one', async () => {
    const onChange = vi.fn<(id: string | null) => void>()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={onChange}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    // Popover renders in a portal; use findBy* to retry until mounted.
    expect(await screen.findByText('Alpha')).not.toBeNull()
    expect(screen.queryByText('Beta')).not.toBeNull()

    fireEvent.click(screen.getByText('Beta'))
    expect(onChange).toHaveBeenCalledWith('prj_b')
  })

  it('selecting the all-row calls onChange(null)', async () => {
    const onChange = vi.fn<(id: string | null) => void>()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value="prj_b"
        onChange={onChange}
        allLabel="All projects"
        ariaLabel="Filter by project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    const allRow = await screen.findByText('All projects')
    fireEvent.click(allRow)
    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('renders a Create footer when onCreate is provided', async () => {
    const onCreate = vi.fn()
    render(
      <FilterEntityCombobox
        icon={<span>P</span>}
        items={ITEMS}
        value={null}
        onChange={() => {}}
        allLabel="All projects"
        ariaLabel="Filter by project"
        onCreate={onCreate}
        createLabel="New project"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /filter by project/i }))
    const createBtn = await screen.findByText('New project')
    fireEvent.click(createBtn)
    expect(onCreate).toHaveBeenCalledTimes(1)
  })
})
