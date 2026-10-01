/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { ApiProvider } from '@/hooks/use-api'
import { TorqueApiClient } from '@/lib/api'
import type { TaskSummary } from '@/lib/types'
import { usePagedTaskSummaries } from './use-paged-task-summaries'

;(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const task = (n: number) => ({ id: `T-${n}` }) as TaskSummary

function setup(total: number) {
  const client = new TorqueApiClient('/api/v1')
  const listTaskSummaries = vi
    .spyOn(client, 'listTaskSummaries')
    .mockImplementation(async (filter) => {
      const offset = filter?.offset ?? 0
      const limit = filter?.limit ?? 50
      const tasks = Array.from({ length: Math.max(0, Math.min(limit, total - offset)) }, (_, i) => task(offset + i))
      return { tasks, total }
    })
  const wrapper = ({ children }: { children: ReactNode }) => <ApiProvider client={client}>{children}</ApiProvider>
  const hook = renderHook(() => usePagedTaskSummaries({ project_id: 'PRJ-1' }, 2), { wrapper })
  return { hook, listTaskSummaries }
}

describe('usePagedTaskSummaries', () => {
  it('fetches one page, then the next on loadMore, newest update first', async () => {
    const { hook, listTaskSummaries } = setup(5)

    await act(() => hook.result.current.reload())
    expect(hook.result.current.tasks.map((t) => t.id)).toEqual(['T-0', 'T-1'])
    expect(hook.result.current.total).toBe(5)
    expect(hook.result.current.hasMore).toBe(true)

    await act(() => hook.result.current.loadMore())
    expect(hook.result.current.tasks.map((t) => t.id)).toEqual(['T-0', 'T-1', 'T-2', 'T-3'])

    expect(listTaskSummaries.mock.calls.map((c) => c[0])).toEqual([
      { project_id: 'PRJ-1', epic_id: undefined, sprint_id: undefined, sort_by: 'updated_at', sort_dir: 'desc', offset: 0, limit: 2 },
      { project_id: 'PRJ-1', epic_id: undefined, sprint_id: undefined, sort_by: 'updated_at', sort_dir: 'desc', offset: 2, limit: 2 },
    ])
  })

  it('reload keeps as many rows as are already shown', async () => {
    const { hook, listTaskSummaries } = setup(5)
    await act(() => hook.result.current.reload())
    await act(() => hook.result.current.loadMore())
    await act(() => hook.result.current.reload())

    expect(listTaskSummaries.mock.calls.at(-1)?.[0]).toMatchObject({ offset: 0, limit: 4 })
    expect(hook.result.current.tasks).toHaveLength(4)
  })
})
