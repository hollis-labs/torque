/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { ApiProvider } from '@/hooks/use-api'
import { TorqueApiClient } from '@/lib/api'
import type { ListPage, SSEEvent, Task, TaskSummary } from '@/lib/types'
import { usePagedTaskSummaries } from './use-paged-task-summaries'

const task = (n: number) => ({ id: `T-${n}`, project_id: 'PRJ-1', status: 'todo' }) as TaskSummary
const page = (tasks: TaskSummary[], next: string | null = null): ListPage<TaskSummary> => ({
  items: tasks, meta: { returned: tasks.length, limit: 2, has_more: next !== null, next_cursor: next },
})
function setup() {
  const client = new TorqueApiClient('/api/v1')
  let event!: (event: SSEEvent) => void
  const unsubscribe = vi.fn()
  vi.spyOn(client, 'subscribeEvents').mockImplementation(callback => { event = callback; return unsubscribe })
  const fetchPage = vi.spyOn(client, 'listTaskSummaryPage').mockResolvedValueOnce(page([task(0), task(1)], 'next')).mockResolvedValue(page([task(2)]))
  const getTask = vi.spyOn(client, 'getTask').mockResolvedValue({ ...task(0), title: 'Changed title' } as Awaited<ReturnType<typeof client.getTask>>)
  const invalidate = vi.fn()
  const wrapper = ({ children }: { children: ReactNode }) => <ApiProvider client={client}>{children}</ApiProvider>
  const hook = renderHook(() => usePagedTaskSummaries({ project_id: 'PRJ-1' }, 2, invalidate), { wrapper })
  return { hook, fetchPage, getTask, invalidate, emit: (data: SSEEvent) => event(data), unsubscribe }
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers() })

describe('usePagedTaskSummaries', () => {
  it('loads one summary page automatically and continues by cursor, without requesting total', async () => {
    const { hook, fetchPage } = setup()
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(2))
    expect(hook.result.current.total).toBeUndefined()
    expect(fetchPage.mock.calls[0][0]).toEqual({ project_id: 'PRJ-1', sort_by: 'updated_at', sort_dir: 'desc', limit: 2, cursor: undefined })
    await act(() => hook.result.current.loadMore())
    expect(fetchPage.mock.calls[1][0]).toMatchObject({ cursor: 'next', limit: 2 })
    expect(hook.result.current.tasks.map(task => task.id)).toEqual(['T-0', 'T-1', 'T-2'])
    expect(hook.result.current.hasMore).toBe(false)
    await act(() => hook.result.current.reload())
    expect(fetchPage.mock.calls[2][0]).toMatchObject({ cursor: undefined, limit: 2 })
    expect(fetchPage.mock.calls[2][0]).not.toHaveProperty('offset')
  })

  it('patches a visible transition immediately; unknown updates only invalidate counts', async () => {
    const { hook, fetchPage, getTask, emit, invalidate } = setup()
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(2))
    vi.useFakeTimers()
    act(() => {
      emit({ type: 'task.transitioned', data: { task_id: 'T-0', status: 'doing' } })
      emit({ type: 'task.updated', data: { task_id: 'unseen' } })
    })
    expect(hook.result.current.tasks[0].status).toBe('doing')
    await act(() => vi.advanceTimersByTimeAsync(1500))
    expect(invalidate).toHaveBeenCalledOnce()
    expect(getTask).not.toHaveBeenCalled()
    expect(fetchPage).toHaveBeenCalledOnce()
  })

  it('coalesces ID-only updates for visible rows into one single-row fetch and removes a row leaving scope', async () => {
    const { hook, fetchPage, getTask, emit } = setup()
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(2))
    vi.useFakeTimers()
    act(() => { for (let i = 0; i < 200; i++) emit({ type: 'task.updated', data: { task_id: 'T-0' } }) })
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(getTask).toHaveBeenCalledOnce()
    expect(hook.result.current.tasks[0].title).toBe('Changed title')
    getTask.mockResolvedValue({ ...task(0), project_id: 'PRJ-2' } as Task)
    act(() => emit({ type: 'task.updated', data: { task_id: 'T-0' } }))
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(hook.result.current.tasks.map(task => task.id)).toEqual(['T-1'])
    expect(fetchPage).toHaveBeenCalledOnce()
  })

  it('disconnects the SSE adapter on unmount', async () => {
    const { hook, unsubscribe } = setup()
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(2))
    hook.unmount()
    expect(unsubscribe).toHaveBeenCalledOnce()
  })
})
