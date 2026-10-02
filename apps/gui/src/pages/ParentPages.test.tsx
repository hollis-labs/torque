// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import EpicsPage from './EpicsPage'
import ProjectsPage from './ProjectsPage'
import SprintsPage from './SprintsPage'
import type { ListPage, Epic } from '@/lib/types'
import type { ParentFacetResult } from '@/lib/api'

const api = vi.hoisted(() => ({ listEpics: vi.fn(), listSprints: vi.fn(), listProjects: vi.fn(), parentFacets: vi.fn(), subscribeEvents: vi.fn(), getProject: vi.fn() }))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
const rows = (start: number, size = 50) => Array.from({ length: size }, (_, i) => ({ id: `E${start + i}`, name: `Epic ${start + i}`, description: '', repo_path: '', goal: '', cost_budget: null, approval_mode: 'approve_each', status: 'active', priority: 1, project_id: null, updated_at: '2026-10-01T00:00:00Z', created_at: '2026-10-01T00:00:00Z' } as Epic))
const page = (items: Epic[], cursor: string | null): ListPage<Epic> => ({ items, meta: { returned: items.length, limit: 50, has_more: !!cursor, next_cursor: cursor } })
const facets = (ids: string[]): ParentFacetResult => ({
  matching_count: 340,
  facets: [{ dimension: 'status', buckets: [{ value: 'active', count: 300 }, { value: 'completed', count: 40 }], returned: 2, total_distinct: 2, truncated: false }, { dimension: 'priority', buckets: [{ value: 1, count: 210 }], returned: 1, total_distinct: 1, truncated: false }],
  task_totals: { total: 12345, counts: { todo: 9000, done: 3345 } },
  child_totals: { sprints: 720, epics: 340 },
  task_rollups: { scopes: ids.map(scope_id => ({ scope_id, total: 7, counts: { todo: 4, done: 3 }, children: { sprints: 6, epics: 8 } })), returned: ids.length, total_distinct: ids.length, truncated: false },
})
afterEach(cleanup)
beforeEach(() => {
  vi.resetAllMocks()
  api.listEpics.mockImplementation(query => Promise.resolve(page(rows(query.cursor ? 50 : 0), query.cursor ? null : 'next-50')))
  api.listSprints.mockImplementation(query => Promise.resolve(page(rows(query.cursor ? 50 : 0), query.cursor ? null : 'next-50')))
  api.listProjects.mockImplementation((_status, query) => Promise.resolve(page(rows(query.cursor ? 50 : 0), query.cursor ? null : 'next-50')))
  api.parentFacets.mockImplementation((_kind, _query, ids) => Promise.resolve(facets(ids)))
  api.subscribeEvents.mockReturnValue(() => {})
})
function show(Component: typeof EpicsPage) { render(<MemoryRouter><Component /></MemoryRouter>) }

describe('parent page cohorts', () => {
  it.each([['epic', EpicsPage], ['sprint', SprintsPage], ['project', ProjectsPage]] as const)('uses exact facet cards and only on-screen rollup IDs for %s', async (kind, Component) => {
    show(Component)
    await screen.findByText('Epic 0')
    await screen.findByText('12345')
    await waitFor(() => expect(api.parentFacets).toHaveBeenLastCalledWith(kind, expect.anything(), rows(0).map(row => row.id), expect.any(AbortSignal)))
    expect(api.listEpics.mock.calls.length + api.listProjects.mock.calls.length + api.listSprints.mock.calls.length).toBe(1)
    expect(screen.getAllByText('340').length).toBeGreaterThan(0)
    expect(screen.getAllByText('3/7 (43%)').length).toBe(50)
    if (kind === 'project') { expect(screen.getByText('720')).toBeTruthy(); expect(screen.getAllByText('8').length).toBe(50) }
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    await screen.findByText('Epic 50')
    expect(screen.queryByText('Epic 0')).toBeNull()
    await waitFor(() => expect(api.parentFacets).toHaveBeenLastCalledWith(kind, expect.anything(), rows(50).map(row => row.id), expect.any(AbortSignal)))
    const calls = kind === 'project' ? api.listProjects.mock.calls.map(call => call[1]) : kind === 'epic' ? api.listEpics.mock.calls.map(call => call[0]) : api.listSprints.mock.calls.map(call => call[0])
    expect(calls).toEqual([expect.objectContaining({ limit: 50 }), expect.objectContaining({ limit: 50, cursor: 'next-50' })])
    expect(screen.getByRole('button', { name: 'Next' }).hasAttribute('disabled')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    await screen.findByText('Epic 0')
    expect(api.listEpics.mock.calls.length + api.listProjects.mock.calls.length + api.listSprints.mock.calls.length).toBe(2)
  }, 15000)

  it('sends the same server search/status/archive cohort to rows and facets, resetting navigation', async () => {
    show(EpicsPage)
    await screen.findByText('Epic 0')
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    await screen.findByText('Epic 50')
    fireEvent.change(screen.getByLabelText('Search epics'), { target: { value: 'needle' } })
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'inactive' } })
    fireEvent.click(screen.getByLabelText('Include archived'))
    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'name' } })
    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: 'asc' } })
    await waitFor(() => expect(api.listEpics).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'needle', status: 'inactive', include_archived: true, sort_by: 'name', sort_dir: 'asc', limit: 50 }), expect.any(AbortSignal)))
    await waitFor(() => expect(api.parentFacets).toHaveBeenLastCalledWith('epic', expect.objectContaining({ search: 'needle', status: 'inactive', include_archived: true }), rows(0).map(row => row.id), expect.any(AbortSignal)))
    expect(screen.getByText('Page 1')).toBeTruthy()
    expect(api.listEpics.mock.calls.at(-1)?.[0].cursor).toBeUndefined()
  }, 15000)

  it('keeps the current page on a failed Next request and offers retry', async () => {
    api.listEpics.mockImplementation(query => query.cursor ? Promise.reject(new Error('network failed')) : Promise.resolve(page(rows(0), 'next-50')))
    show(EpicsPage)
    await screen.findByText('Epic 0')
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    await screen.findByText('network failed')
    expect(screen.getByText('Page 1')).toBeTruthy()
  })
  it('debounces SSE facet refresh without fetching row pages', async () => {
    show(EpicsPage)
    await screen.findByText('Epic 0')
    await waitFor(() => expect(api.parentFacets.mock.calls.at(-1)?.[2]).toHaveLength(50))
    const before = api.parentFacets.mock.calls.length
    const notify = api.subscribeEvents.mock.calls.at(-1)?.[0]
    await act(async () => {
      for (let i = 0; i < 5; i++) notify({ type: 'task.transitioned', data: { epic_id: 'E0' } })
      await new Promise(resolve => setTimeout(resolve, 1700))
    })
    await waitFor(() => expect(api.parentFacets.mock.calls.length).toBe(before + 1))
    expect(api.listEpics.mock.calls.length).toBe(1)
    expect(screen.getByText(/Scopes changed/)).toBeTruthy()
    expect(api.parentFacets.mock.calls.at(-1)?.[2]).toEqual(rows(0).map(row => row.id))
  }, 15000)

})
