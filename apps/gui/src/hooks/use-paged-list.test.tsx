/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor, cleanup } from '@testing-library/react'
import { usePagedList, type PageRequest } from './use-paged-list'
import type { ListPage } from '@/lib/types'

type Row = { id: number; status: string }
type Params = { search?: string; sort_by?: string; limit?: number; include_total?: boolean }
const row = (id: number, status = 'todo'): Row => ({ id, status })
const getId = (item: Row) => item.id
const page = (items: Row[], next: string | null = null, total?: number): ListPage<Row> => ({
  items, meta: { returned: items.length, limit: 2, has_more: next !== null, next_cursor: next, ...(total === undefined ? {} : { total }) },
})
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { resolve, promise }
}
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('usePagedList', () => {
  it('requests one cursor page and follows its cursor only on loadMore; deduplicates overlap', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce(page([row(1), row(2)], 'opaque')).mockResolvedValueOnce(page([row(2), row(3)]))
    const { result } = renderHook(() => usePagedList({ fetchPage, params: { limit: 2 }, getId }))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(result.current.total).toBeUndefined()
    expect(result.current.hasMore).toBe(true)
    expect(fetchPage.mock.calls[0][0]).not.toHaveProperty('offset')
    await act(() => result.current.loadMore())
    expect(fetchPage.mock.calls[1][0]).toMatchObject({ cursor: 'opaque', params: { limit: 2 } })
    expect(result.current.items.map(getId)).toEqual([1, 2, 3])
    expect(result.current.hasMore).toBe(false)
    await act(() => result.current.loadMore())
    expect(fetchPage).toHaveBeenCalledTimes(2)
  })

  it('starts offset mode explicitly at zero and follows next_offset without any cursor', async () => {
    const first = page([row(1), row(2)])
    first.meta = { ...first.meta, has_more: true, offset: 0, next_offset: 2 }
    const fetchPage = vi.fn().mockResolvedValueOnce(first).mockResolvedValueOnce(page([row(3)]))
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId, mode: 'offset' }))
    await waitFor(() => expect(result.current.loading).toBe(false))
    await act(() => result.current.loadMore())
    expect(fetchPage.mock.calls.map(([request]) => request.offset)).toEqual([0, 2])
    expect(fetchPage.mock.calls.every(([request]) => !('cursor' in request))).toBe(true)
  })

  it('aborts query generations and rejects late success even when transport ignores abort', async () => {
    const old = deferred<ListPage<Row>>()
    const fetchPage = vi.fn(({ params }: PageRequest<Params>) => params.search === 'old' ? old.promise : Promise.resolve(page([row(2)])))
    const { result, rerender } = renderHook(({ search }) => usePagedList({ fetchPage, params: { search }, getId }), { initialProps: { search: 'old' } })
    const oldSignal = fetchPage.mock.calls[0][0].signal
    rerender({ search: 'new' })
    await waitFor(() => expect(result.current.items).toEqual([row(2)]))
    expect(oldSignal.aborted).toBe(true)
    await act(async () => old.resolve(page([row(1)], 'old-cursor', 100)))
    expect(result.current.items).toEqual([row(2)])
    expect(result.current.total).toBeUndefined()
    expect(result.current.hasMore).toBe(false)
  })

  it('resets on sort changes, passes include_total only as requested, and ignores equivalent param order', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page([row(1)], null, 10))
    const { result, rerender } = renderHook(({ params }: { params: Params }) => usePagedList({ fetchPage, params, getId }), {
      initialProps: { params: { sort_by: 'status', include_total: true } },
    })
    await waitFor(() => expect(result.current.total).toBe(10))
    rerender({ params: { include_total: true, sort_by: 'status' } })
    expect(fetchPage).toHaveBeenCalledTimes(1)
    fetchPage.mockResolvedValue(page([row(2)]))
    rerender({ params: { sort_by: 'updated_at' } })
    await waitFor(() => expect(result.current.items).toEqual([row(2)]))
    expect(result.current.total).toBeUndefined()
    expect(fetchPage.mock.calls[1][0].params).not.toHaveProperty('include_total')
  })

  it('guards duplicate loadMore calls and surfaces malformed continuation without advancing', async () => {
    const next = deferred<ListPage<Row>>()
    const fetchPage = vi.fn().mockResolvedValueOnce(page([row(1)], 'cursor')).mockReturnValueOnce(next.promise)
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId }))
    await waitFor(() => expect(result.current.hasMore).toBe(true))
    let pending!: Promise<void>
    act(() => { pending = result.current.loadMore(); void result.current.loadMore() })
    expect(fetchPage).toHaveBeenCalledTimes(2)
    await act(async () => { next.resolve(page([row(2)], 'cursor')); await pending })
    expect(result.current.error?.message).toMatch(/forward cursor/)
    expect(result.current.items).toEqual([row(1)])
    expect(result.current.loadingMore).toBe(false)
  })

  it('patches/removes only visible rows, including rows that leave the filter; unknown IDs invalidate once', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page([row(1), row(2)]))
    const invalidate = vi.fn()
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId, onInvalidate: invalidate, matches: item => item.status !== 'done' }))
    await waitFor(() => expect(result.current.items).toHaveLength(2))
    vi.useFakeTimers()
    act(() => {
      result.current.applyEvent({ id: 1, patch: { status: 'doing' } })
      result.current.applyEvent({ id: 2, patch: { status: 'done' } })
      for (let id = 100; id < 300; id++) result.current.applyEvent({ id, item: row(id) })
    })
    expect(result.current.items).toEqual([row(1, 'doing')])
    await act(() => vi.advanceTimersByTimeAsync(1500))
    expect(invalidate).toHaveBeenCalledTimes(1)
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  it('coalesces 200 events over ten loaded IDs into ten row fetches and no list fetches', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(Array.from({ length: 10 }, (_, id) => row(id))))
    const fetchItem = vi.fn(async (id: string | number) => row(Number(id), 'doing'))
    const invalidate = vi.fn()
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId, fetchItem, onInvalidate: invalidate }))
    await waitFor(() => expect(result.current.items).toHaveLength(10))
    vi.useFakeTimers()
    act(() => { for (let n = 0; n < 200; n++) result.current.applyEvent({ id: n % 10, refresh: true }) })
    await act(() => vi.advanceTimersByTimeAsync(1500))
    expect(fetchItem).toHaveBeenCalledTimes(10)
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(invalidate).toHaveBeenCalledTimes(1)
    expect(result.current.items.every(item => item.status === 'doing')).toBe(true)
  })

  it('marks a 100-ID burst stale without fanout and invalidates aggregates once', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(Array.from({ length: 100 }, (_, id) => row(id))))
    const fetchItem = vi.fn()
    const invalidate = vi.fn()
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId, fetchItem, onInvalidate: invalidate }))
    await waitFor(() => expect(result.current.items).toHaveLength(100))
    vi.useFakeTimers()
    act(() => { for (let id = 0; id < 100; id++) result.current.applyEvent({ id, refresh: true }) })
    await act(() => vi.advanceTimersByTimeAsync(1500))
    expect(result.current.isStale).toBe(true)
    expect(fetchItem).not.toHaveBeenCalled()
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(invalidate).toHaveBeenCalledTimes(1)
    await act(() => result.current.refresh())
    expect(result.current.isStale).toBe(false)
    expect(fetchPage).toHaveBeenCalledTimes(2)
  })

  it('bounds row concurrency across overlapping bursts and rejects stale row refreshes after a direct patch', async () => {
    const responses = Array.from({ length: 8 }, () => deferred<Row>())
    const fetchPage = vi.fn().mockResolvedValue(page(Array.from({ length: 8 }, (_, id) => row(id))))
    const fetchItem = vi.fn((id: string | number) => responses[Number(id)].promise)
    const { result } = renderHook(() => usePagedList({ fetchPage, params: {}, getId, fetchItem }))
    await waitFor(() => expect(result.current.items).toHaveLength(8))
    vi.useFakeTimers()
    act(() => { for (let id = 0; id < 4; id++) result.current.applyEvent({ id, refresh: true }) })
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(fetchItem).toHaveBeenCalledTimes(4)
    act(() => {
      result.current.applyEvent({ id: 0, patch: { status: 'done' } })
      for (let id = 4; id < 8; id++) result.current.applyEvent({ id, refresh: true })
    })
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(fetchItem).toHaveBeenCalledTimes(4)
    await act(async () => { for (let id = 0; id < 4; id++) responses[id].resolve(row(id, 'doing')) })
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(fetchItem).toHaveBeenCalledTimes(8)
    expect(result.current.items[0].status).toBe('done')
    await act(async () => { for (let id = 4; id < 8; id++) responses[id].resolve(row(id, 'doing')) })
  })

  it('aborts row refreshes and subscriptions on query changes and unmount', async () => {
    const oldRow = deferred<Row>()
    const fetchPage = vi.fn().mockResolvedValue(page([row(1)]))
    const fetchItem = vi.fn((_id: string | number, _request: { signal: AbortSignal }) => oldRow.promise)
    const unsubscribe = vi.fn()
    const subscribe = vi.fn(() => unsubscribe)
    const { result, rerender, unmount } = renderHook(({ search }) => usePagedList({ fetchPage, params: { search }, getId, fetchItem, subscribe }), { initialProps: { search: 'old' } })
    await waitFor(() => expect(result.current.items).toHaveLength(1))
    vi.useFakeTimers()
    act(() => result.current.applyEvent({ id: 1, refresh: true }))
    await act(() => vi.advanceTimersByTimeAsync(200))
    const signal = fetchItem.mock.calls[0][1].signal
    await act(async () => rerender({ search: 'new' }))
    expect(signal.aborted).toBe(true)
    await act(async () => oldRow.resolve(row(1, 'done')))
    expect(result.current.items[0].status).toBe('todo')
    unmount()
    expect(unsubscribe).toHaveBeenCalledOnce()
  })
})
