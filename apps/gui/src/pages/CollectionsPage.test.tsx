/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { ListPage, Task, Collection } from '@/lib/types'
const { listCollections, listCollectionTasks, listInboxTasks } = vi.hoisted(() => ({ listCollections: vi.fn(), listCollectionTasks: vi.fn(), listInboxTasks: vi.fn() }))
vi.mock('@/hooks/use-api', () => ({ useApi: () => ({ listCollections, listCollectionTasks, listInboxTasks }) }))
vi.mock('@/hooks/use-sse', () => ({ useSSE: () => ({ lastEvent: null }) }))
import CollectionsPage from './CollectionsPage'
function page<T>(items: T[], total: number, next_cursor: string | null): ListPage<T> {
  return { items, meta: { returned: items.length, total, limit: 50, has_more: !!next_cursor, next_cursor } }
}
const collection = (id: string): Collection => ({ id, name: id, description: '', created_at: '2026-10-01', updated_at: '2026-10-01', archived_at: null })
const tasks = (start: number) => Array.from({ length: 50 }, (_, index) => ({ id: `T-${start + index}`, title: `Task ${start + index}`, priority: 2, status: 'todo', collection_id: 'COL-1' }) as Task)
afterEach(() => { cleanup(); vi.resetAllMocks() })
it('pages large collections and task cohorts only on explicit continuation', async () => {
  listCollections.mockResolvedValueOnce(page([collection('COL-1')], 75, 'collections-next')).mockResolvedValueOnce(page([collection('COL-2')], 75, null))
  listCollectionTasks.mockImplementation((id, query) => Promise.resolve(id === 'COL-1' ? page(tasks(query.cursor ? 50 : 0), 301, query.cursor ? null : 'tasks-next') : page([], 0, null)))
  listInboxTasks.mockResolvedValue(page([], 0, null))
  render(<MemoryRouter><CollectionsPage /></MemoryRouter>)
  await screen.findByText('Task 49')
  expect(listCollections).toHaveBeenCalledTimes(1)
  expect(listCollectionTasks).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('Task 50')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Load more tasks' }))
  await screen.findByText('Task 99')
  expect(listCollectionTasks.mock.calls[1][1]).toMatchObject({ cursor: 'tasks-next', sort_by: 'position', sort_dir: 'asc', include_total: true })
  expect(screen.getByText('Task 0')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Load more collections' }))
  await waitFor(() => expect(listCollectionTasks).toHaveBeenCalledTimes(3))
  expect(listCollections.mock.calls[1][1]).toMatchObject({ cursor: 'collections-next', sort_by: 'created_at' })
})
