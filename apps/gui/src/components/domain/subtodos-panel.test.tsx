/** @vitest-environment jsdom */
import { render, fireEvent, screen, waitFor, cleanup } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import { TorqueApiClient } from '@/lib/api'
import { SubtodosPanel } from './subtodos-panel'
import type { Subtodo } from '@/lib/types'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })
it('renders one checklist page, continues only on click and reloads a page after mutation', async () => {
  const items: Subtodo[] = Array.from({ length: 205 }, (_, i) => ({ id: `check-${i}`, text: `Check ${i}`, required: false, done: false, evidence: '' }))
  const requests: URL[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://test'); requests.push(url)
    const start = url.searchParams.get('cursor') ? 50 : 0
    const body = init?.method === 'POST' ? { subtodos: items.map((item, i) => ({ ...item, done: i === 0 })) }
      : { items: items.slice(start, start + 50), meta: { returned: 50, limit: 50, has_more: true, next_cursor: `after-${start + 50}`, total: 205 } }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })
  }))
  const onChange = vi.fn()
  render(<ApiProvider client={new TorqueApiClient('/api/v1')}><SubtodosPanel taskId="T-1" subtodos={items} onChange={onChange} /></ApiProvider>)
  await waitFor(() => expect(screen.getByText('Check 49')).toBeTruthy())
  expect(screen.queryByText('Check 50')).toBeNull()
  expect(requests).toHaveLength(1)
  expect(requests[0].searchParams.get('limit')).toBe('50')
  expect(requests[0].searchParams.get('include_total')).toBe('true')
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(screen.getByText('Check 99')).toBeTruthy())
  expect(requests).toHaveLength(2)
  expect(requests[1].searchParams.get('cursor')).toBe('after-50')
  fireEvent.click(screen.getAllByRole('checkbox')[0])
  await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(requests).toHaveLength(4))
  expect(requests[3].searchParams.has('cursor')).toBe(false)
  await waitFor(() => expect(screen.queryByText('Check 50')).toBeNull())
  expect(screen.queryByText('Check 204')).toBeNull()
})
