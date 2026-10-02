// @vitest-environment jsdom
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ApiProvider } from '@/hooks/use-api'
import { ScopePicker } from './scope-picker'

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
afterEach(cleanup)
beforeEach(() => {
  vi.restoreAllMocks()
  Element.prototype.scrollIntoView = vi.fn()
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
})
it('searches one server page and retains a selected ID outside that page', async () => {
  const requested: URL[] = []
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost')
    requested.push(url)
    if (url.pathname.endsWith('/projects/selected')) return json({ id: 'selected', name: 'Selected off page' })
    if (url.pathname.endsWith('/projects/found')) return json({ id: 'found', name: 'Remote needle' })
    if (url.pathname.endsWith('/projects')) {
      const searched = url.searchParams.get('search') === 'needle'
      return json({ items: searched ? [{ id: 'found', name: 'Remote needle' }] : [{ id: 'first', name: 'First' }], meta: { returned: 1, limit: 50, has_more: !searched, next_cursor: searched ? null : 'more' } })
    }
    throw new Error(`Unexpected URL ${url}`)
  }) as typeof fetch
  function Picker() {
    const [value, setValue] = useState<string | null>('selected')
    return <ScopePicker kind="project" value={value} onChange={setValue} />
  }
  render(<ApiProvider><Picker /></ApiProvider>)
  await screen.findByText('Selected off page')
  expect(requested.filter(url => url.pathname.endsWith('/projects')).length).toBe(0)
  fireEvent.click(screen.getByRole('combobox', { name: 'Choose project' }))
  await screen.findByText('More than 50 projects match. Type to search.')
  expect(screen.queryByText('First')).toBeNull()
  expect(screen.getByText('Selected off page')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Search projects'), { target: { value: 'needle' } })
  await screen.findByText('Remote needle')
  const lists = requested.filter(url => url.pathname.endsWith('/projects'))
  expect(lists.length).toBe(2)
  expect(lists.every(url => url.searchParams.get('limit') === '50' && !url.searchParams.has('cursor') && !url.searchParams.has('include_total'))).toBe(true)
  expect(lists[1].searchParams.get('search')).toBe('needle')
  fireEvent.click(screen.getByText('Remote needle'))
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Choose project' }).textContent).toContain('Remote needle'))
})
it('scopes epic search on the server and ignores an obsolete search response', async () => {
  let resolveOld: ((response: Response) => void) | undefined
  const requested: URL[] = []
  globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost')
    requested.push(url)
    if (url.searchParams.get('search') === 'old') return new Promise<Response>(resolve => { resolveOld = resolve })
    return Promise.resolve(json({ items: [{ id: 'new', name: 'New result' }], meta: { returned: 1, limit: 50, has_more: false, next_cursor: null } }))
  }) as typeof fetch
  render(<ApiProvider><ScopePicker kind="epic" projectId="P-other-page" onChange={() => {}} /></ApiProvider>)
  fireEvent.click(screen.getByRole('combobox', { name: 'Choose epic' }))
  await screen.findByText('New result')
  fireEvent.change(screen.getByLabelText('Search epics'), { target: { value: 'old' } })
  await waitFor(() => expect(resolveOld).toBeDefined())
  fireEvent.change(screen.getByLabelText('Search epics'), { target: { value: 'new' } })
  await screen.findByText('New result')
  resolveOld!(json({ items: [{ id: 'old', name: 'Obsolete result' }], meta: { returned: 1, limit: 50, has_more: false, next_cursor: null } }))
  await waitFor(() => expect(screen.queryByText('Obsolete result')).toBeNull())
  expect(requested.every(url => url.searchParams.get('project_id') === 'P-other-page')).toBe(true)
})
