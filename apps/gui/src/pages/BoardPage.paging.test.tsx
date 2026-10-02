/** @vitest-environment jsdom */
import { act } from 'react'
import { render, fireEvent, screen, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@/hooks/use-api'
import BoardPage from './BoardPage'
import type { TaskSummary } from '@/lib/types'
import { readOpsViews } from '@/lib/ops-saved-views'
;(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('@/components/domain/task-table', () => ({ TaskTable: ({ tasks, onLoadMore, onSortChange }: {
  tasks: TaskSummary[]; onLoadMore: () => void; onSortChange: (key: string, dir: string) => void
}) => <div><output aria-label="Loaded rows">{tasks.map(task => task.id).join(',')}</output>
  <button onClick={onLoadMore}>Next rows</button><button onClick={() => onSortChange('priority', 'asc')}>Sort priority</button></div> }))
vi.mock('@/components/domain/scheduler-toggle-button', () => ({ SchedulerToggleButton: () => null }))
vi.mock('@/components/domain/restart-frontend-button', () => ({ RestartFrontendButton: () => null }))

const requests: URL[] = []
const sources = new Set<FakeEventSource>()
class FakeEventSource {
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor() { sources.add(this) }
  close() { sources.delete(this) }
}
function emit(id: string) {
  for (const source of sources) source.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'task.updated', data: { task_id: id } }) }))
}
function mount(entry = '/operations') {
  return render(<ApiProvider><MemoryRouter initialEntries={[entry]}><BoardPage /></MemoryRouter></ApiProvider>)
}
function taskQueries() { return requests.filter(url => url.pathname.endsWith('/tasks')) }
function facetQueries() { return requests.filter(url => url.pathname.endsWith('/tasks/facets')) }
beforeEach(() => {
  localStorage.clear(); requests.length = 0; sources.clear()
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.stubGlobal('ResizeObserver', class { observe() {}; unobserve() {}; disconnect() {} })
  Element.prototype.scrollIntoView = () => {}
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost'); requests.push(url)
    let body: unknown = {}
    if (url.pathname.endsWith('/tasks/facets')) body = { matching_count: 4001, facets: [{ dimension: 'status', buckets: [{ value: 'todo', count: 4001 }] }] }
    else if (url.pathname.endsWith('/tasks')) {
      const start = url.searchParams.has('cursor') ? 50 : 0
      body = { items: Array.from({ length: 50 }, (_, n) => ({ id: `T-${start+n}`, title: `Task ${start+n}`, status: 'todo', kind: 'agent', manual: false, priority: 1, tags: [] })),
        meta: { returned: 50, limit: 50, has_more: true, next_cursor: `cursor-${start+50}` } }
    } else if (/\/tasks\/T-/.test(url.pathname)) body = { id: url.pathname.split('/').pop(), status: 'todo', kind: 'agent', manual: false, priority: 1, tags: [] }
    else if (/\/(projects|sprints|epics)$/.test(url.pathname)) body = { items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null } }
    else if (url.pathname.endsWith('/tags')) body = { items: [], meta: { returned: 0, limit: 50, has_more: false, next_cursor: null } }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })
  }))
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

describe('Board server paging and cohorts', () => {
  it('loads one bounded page, displays full facet counts, continues by cursor and resets on server sort', async () => {
    mount()
    await waitFor(() => expect(screen.getByLabelText('Loaded rows').textContent?.split(',')).toHaveLength(50))
    expect(taskQueries()).toHaveLength(1)
    expect(taskQueries()[0].searchParams.get('limit')).toBe('50')
    expect(taskQueries()[0].searchParams.has('include_total')).toBe(false)
    expect(screen.getByText('4001')).toBeTruthy()
    fireEvent.click(screen.getByText('Next rows'))
    await waitFor(() => expect(screen.getByLabelText('Loaded rows').textContent?.split(',')).toHaveLength(100))
    expect(taskQueries()[1].searchParams.get('cursor')).toBe('cursor-50')
    fireEvent.click(screen.getByText('Sort priority'))
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('sort_by')).toBe('priority'))
    expect(taskQueries().at(-1)?.searchParams.has('cursor')).toBe(false)
    await waitFor(() => expect(screen.getByLabelText('Loaded rows').textContent?.split(',')).toHaveLength(50))
  })
  it('searches tag pages on the server and preserves query and color when continuing', async () => {
    const previous = globalThis.fetch
    vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, 'http://test')
      if (!url.pathname.endsWith('/tags')) return previous(input, init)
      requests.push(url)
      return new Response(JSON.stringify({ items: [{ slug: 'tag-result', name: 'Server tag', color: 'blue' }],
        meta: { returned: 1, limit: 50, has_more: !url.searchParams.has('cursor'), next_cursor: url.searchParams.has('cursor') ? null : 'tag-next' } }),
        { status: 200, headers: { 'content-type': 'application/json' } })
    }))
    mount()
    await waitFor(() => expect(requests.filter(url => url.pathname.endsWith('/tags'))).toHaveLength(1))
    fireEvent.click(screen.getByRole('button', { name: 'Filter by tag' }))
    fireEvent.change(screen.getByLabelText('Search tags'), { target: { value: 'needle' } })
    await waitFor(() => expect(requests.filter(url => url.pathname.endsWith('/tags')).at(-1)?.searchParams.get('query')).toBe('needle'))
    fireEvent.change(screen.getByLabelText('Tag color'), { target: { value: 'blue' } })
    await waitFor(() => expect(requests.filter(url => url.pathname.endsWith('/tags')).at(-1)?.searchParams.get('color')).toBe('blue'))
    const before = requests.filter(url => url.pathname.endsWith('/tags')).length
    fireEvent.click(screen.getByRole('button', { name: 'Load more tags' }))
    await waitFor(() => expect(requests.filter(url => url.pathname.endsWith('/tags'))).toHaveLength(before + 1))
    const query = requests.filter(url => url.pathname.endsWith('/tags')).at(-1)!.searchParams
    expect(query.get('query')).toBe('needle'); expect(query.get('color')).toBe('blue'); expect(query.get('cursor')).toBe('tag-next')
    expect(query.has('include_total')).toBe(false)
  })
  it('eligibility replaces working filters for both requests and restores them when disabled', async () => {
    mount('/operations?status=review&manual=manual&project_id=P1')
    await waitFor(() => expect(taskQueries()).toHaveLength(1))
    fireEvent.click(screen.getByRole('button', { name: 'Eligible' }))
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('eligible')).toBe('true'))
    const list = taskQueries().at(-1)!.searchParams; const facets = facetQueries().at(-1)!.searchParams
    for (const key of ['status', 'manual', 'project_id']) { expect(list.has(key)).toBe(false); expect(facets.has(key)).toBe(false) }
    expect(facets.get('eligible')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: 'Eligible' }))
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('manual')).toBe('true'))
    expect(taskQueries().at(-1)?.searchParams.get('project_id')).toBe('P1')
  })
  it('only a loaded-row event marks an opaque cohort stale and never reloads the list', async () => {
    mount('/operations?eligible=1')
    await waitFor(() => expect(screen.getByLabelText('Loaded rows').textContent).toContain('T-0'))
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 50)) })
    act(() => emit('T-unloaded'))
    expect(screen.queryByText('Updated — refresh')).toBeNull()
    act(() => emit('T-0'))
    await waitFor(() => expect(screen.getByText('Updated — refresh')).toBeTruthy())
    expect(taskQueries()).toHaveLength(1)
    await waitFor(() => expect(facetQueries().length).toBeGreaterThan(1), { timeout: 2500 })
    expect(taskQueries()).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Eligible' }))
    await waitFor(() => expect(taskQueries()).toHaveLength(2))
    fireEvent.click(screen.getByRole('button', { name: 'Eligible' }))
    await waitFor(() => expect(taskQueries()).toHaveLength(3))
    expect(screen.queryByText('Updated — refresh')).toBeNull()
  })
  it('debounces search into both cohorts and keeps saved snapshots unchanged until Save over', async () => {
    mount()
    await waitFor(() => expect(taskQueries()).toHaveLength(1))
    const input = screen.getByRole('searchbox')
    fireEvent.change(input, { target: { value: 'needle' } })
    expect(taskQueries()).toHaveLength(1)
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('search')).toBe('needle'))
    expect(facetQueries().at(-1)?.searchParams.get('search')).toBe('needle')
    fireEvent.click(screen.getByRole('button', { name: 'Edit filters and saved views' }))
    fireEvent.change(screen.getByLabelText('New view name'), { target: { value: 'Triage' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save as' }))
    expect(readOpsViews()[0].filters.search).toBe('needle')
    fireEvent.change(input, { target: { value: 'different' } })
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('search')).toBe('different'))
    expect(readOpsViews()[0].filters.search).toBe('needle')
    fireEvent.click(screen.getByRole('button', { name: 'Reset' }))
    expect((input as HTMLInputElement).value).toBe('needle')
    fireEvent.change(input, { target: { value: 'saved edit' } })
    await waitFor(() => expect(taskQueries().at(-1)?.searchParams.get('search')).toBe('saved edit'))
    fireEvent.click(screen.getByRole('button', { name: 'Save over' }))
    expect(readOpsViews()[0].filters.search).toBe('saved edit')
    fireEvent.click(screen.getByRole('button', { name: /Clear/ }))
    expect(readOpsViews()[0].filters.search).toBe('saved edit')
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect((input as HTMLInputElement).value).toBe('saved edit')
  })

})
