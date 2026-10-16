/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { buildPreviewCard, createDefaultRefRegistry, createMockResolver, type KindResolver } from '../refs'
import { AutolinkProvider, Linkified, PreviewCardView, registerRefActions, registerRefDetail, type AutolinkProviderProps } from '.'

afterEach(cleanup)

const TASK = 'CW-20261016-0101'
const fixtures = { [TASK]: { title: 'Reference registry', status: { label: 'in progress', tone: 'info' as const }, fields: [{ label: 'Priority', value: 'P1' }], updatedAt: '2026-10-16T05:00:00Z' } }

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}

function setup(ui: React.ReactNode, props: Partial<AutolinkProviderProps> = {}) {
  const registry = createDefaultRefRegistry()
  return render(
    <MemoryRouter>
      <AutolinkProvider registry={registry} {...props}>{ui}</AutolinkProvider>
      <Where />
    </MemoryRouter>,
  )
}

describe('Linkified', () => {
  it('renders chips for references and leaves code alone', () => {
    setup(<Linkified text={`see ${TASK} and @claude-code, not \`EP-20261016-0002\``} />)
    const chips = document.querySelectorAll('[data-ref-kind]')
    expect([...chips].map((c) => c.getAttribute('data-ref-kind'))).toEqual(['torque.task', 'agent'])
    expect(document.body.textContent).toContain('`EP-20261016-0002`')
  })

  it('degrades to plain links outside a provider', () => {
    render(<MemoryRouter><Linkified text={`${TASK} https://example.com/x`} /></MemoryRouter>)
    const links = document.querySelectorAll('a')
    expect([...links].map((a) => a.getAttribute('href'))).toEqual([`/tasks/${TASK}`, 'https://example.com/x'])
  })

  it('batches every chip on a page into one call per kind', async () => {
    const onCall = vi.fn()
    const resolver = createMockResolver(fixtures, { onCall })
    setup(
      <>
        <Linkified text={`${TASK} CW-20261016-0102`} />
        <Linkified text={`again ${TASK} and CW-20261016-0103`} />
      </>,
      { resolvers: { 'torque.task': resolver } },
    )
    await waitFor(() => expect(document.querySelector('[data-ref-state="found"]')).not.toBeNull())
    expect(onCall).toHaveBeenCalledTimes(1)
    expect(onCall.mock.calls[0][0].sort()).toEqual([TASK, 'CW-20261016-0102', 'CW-20261016-0103'])
    expect(document.querySelectorAll('[data-ref-state="not-found"]').length).toBe(2)
  })

  it('shows the hovercard on keyboard focus and closes it with Escape', async () => {
    setup(<Linkified text={TASK} chip={{ hoverDelay: 0 }} />, { resolvers: { 'torque.task': createMockResolver(fixtures) } })
    const chip = document.querySelector('[data-ref-kind]') as HTMLElement
    await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
    act(() => chip.focus())
    const card = await screen.findByTestId('ref-hovercard')
    expect(card.textContent).toContain('Reference registry')
    expect(card.textContent).toContain('in progress')
    fireEvent.keyDown(document.activeElement ?? chip, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('ref-hovercard')).toBeNull())
  })

  it('opens the drawer on click and closes it with Escape', async () => {
    setup(<Linkified text={TASK} />, { resolvers: { 'torque.task': createMockResolver(fixtures) } })
    const chip = document.querySelector('[data-ref-kind]') as HTMLElement
    await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
    fireEvent.click(chip)
    const drawer = await screen.findByTestId('ref-drawer')
    expect(drawer.textContent).toContain('Reference registry')
    expect(drawer.textContent).toContain('Priority')
    expect(screen.getByTestId('where').textContent).toBe('/')
    fireEvent.keyDown(drawer, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('ref-drawer')).toBeNull())
  })

  it('navigates straight to the page when the kind has no resolver', () => {
    setup(<Linkified text={TASK} />)
    fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement)
    expect(screen.getByTestId('where').textContent).toBe(`/tasks/${TASK}`)
    expect(screen.queryByTestId('ref-drawer')).toBeNull()
  })

  it('leaves modified clicks to the browser', () => {
    setup(<Linkified text={TASK} />, { resolvers: { 'torque.task': createMockResolver(fixtures) } })
    const stop = (e: Event) => e.preventDefault() // jsdom cannot navigate
    document.addEventListener('click', stop)
    fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement, { ctrlKey: true })
    document.removeEventListener('click', stop)
    expect(screen.queryByTestId('ref-drawer')).toBeNull()
    expect(screen.getByTestId('where').textContent).toBe('/')
  })

  it('marks not-found refs and still opens a useful drawer', async () => {
    setup(<Linkified text="CW-20261016-0999" />, { resolvers: { 'torque.task': createMockResolver(fixtures) } })
    const chip = document.querySelector('[data-ref-kind]') as HTMLElement
    await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('not-found'))
    fireEvent.click(chip)
    const drawer = await screen.findByTestId('ref-drawer')
    expect(drawer.textContent).toContain('Nothing matches')
    expect(drawer.textContent).toContain('Open page')
  })

  it('uses registered actions and detail renderers for a kind', async () => {
    const resolver: KindResolver = { resolveMany: async (ids) => Object.fromEntries(ids.map((i) => [i, { status: 'found' as const, entity: { title: 'T' } }])) }
    const un1 = registerRefActions('torque.task', (ref) => [{ id: 'x', label: 'Extra', type: 'copy', text: ref.canonical }])
    const un2 = registerRefDetail('torque.task', ({ card }) => <div>custom detail for {card.canonical}</div>)
    try {
      setup(<Linkified text={TASK} />, { resolvers: { 'torque.task': resolver } })
      const chip = document.querySelector('[data-ref-kind]') as HTMLElement
      await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
      fireEvent.click(chip)
      const drawer = await screen.findByTestId('ref-drawer')
      expect(drawer.textContent).toContain(`custom detail for ${TASK}`)
      expect(drawer.textContent).toContain('Extra')
    } finally {
      un1()
      un2()
    }
  })
})

describe('PreviewCardView', () => {
  const registry = createDefaultRefRegistry()
  const ref = { kind: 'torque.task', canonical: TASK }

  it('renders loading, error with retry, and unresolved phases', () => {
    const retry = vi.fn()
    const { rerender } = render(<PreviewCardView card={buildPreviewCard(registry, ref, { status: 'loading' })} />)
    expect(screen.getByTestId('ref-card').getAttribute('data-phase')).toBe('loading')
    rerender(<PreviewCardView card={buildPreviewCard(registry, ref, { status: 'error', message: 'boom', fetchedAt: 0 })} onRetry={retry} />)
    fireEvent.click(screen.getByText('Retry'))
    expect(retry).toHaveBeenCalled()
    expect(screen.getByRole('alert').textContent).toContain('boom')
    rerender(<PreviewCardView card={buildPreviewCard(registry, ref, { status: 'unsupported' })} />)
    expect(screen.getByText(/No preview available/)).toBeTruthy()
  })

  it('shows stale data while a refresh is loading', () => {
    const card = buildPreviewCard(registry, ref, { status: 'loading', stale: { title: 'Old title' } })
    render(<PreviewCardView card={card} />)
    expect(screen.getByText('Old title')).toBeTruthy()
  })
})
