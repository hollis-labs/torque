import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ListPage } from '@/lib/types'
import { createTrailingDebounce, EVENT_REFETCH_DEBOUNCE_MS } from '@/lib/debounce'

export type PagePosition = { cursor?: string; offset?: never } | { offset: number; cursor?: never }
export type PageRequest<P> = PagePosition & { params: P; signal: AbortSignal }
export interface ListChange<T> {
  id: string | number
  /** Complete replacement or partial fields for a row already loaded. */
  item?: T
  patch?: Partial<T>
  remove?: boolean
  /** Refresh this loaded row through fetchItem, without fetching the list. */
  refresh?: boolean
}
export interface PagedListOptions<T, P> {
  fetchPage: (request: PageRequest<P>) => Promise<ListPage<T>>
  /** JSON-serializable query/sort/filter, without cursor or offset. */
  params: P & { cursor?: never; offset?: never }
  getId: (item: T) => string | number
  mode?: 'cursor' | 'offset'
  /** Offset mode only; zero is sent explicitly on the first request. */
  initialOffset?: number
  enabled?: boolean
  /** Change when switching transport/resource without changing params. */
  queryKey?: string
  subscribe?: (onChange: (change: ListChange<T>) => void) => () => void
  onInvalidate?: () => void
  invalidateDelayMs?: number
  /** Remove patched rows which no longer match the current filter. */
  matches?: (item: T) => boolean
  fetchItem?: (id: string | number, request: { params: P; signal: AbortSignal }) => Promise<T | null>
  rowRefreshDelayMs?: number
  rowRefreshLimit?: number
  rowRefreshConcurrency?: number
}

interface ListState<T> {
  items: T[]
  meta?: ListPage<T>['meta']
  loading: boolean
  loadingMore: boolean
  error: Error | null
  isStale: boolean
}

// Object key order and freshly allocated equivalent params do not reset a list.
function queryJSON(value: unknown): string {
  return JSON.stringify(value, (_key, entry: unknown) => {
    if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
      return Object.fromEntries(Object.entries(entry).sort(([a], [b]) => a.localeCompare(b)))
    }
    return entry
  })
}

/** Loads exactly one page per request. SSE never fetches row pages. */
export function usePagedList<T, P>(options: PagedListOptions<T, P>) {
  const { mode = 'cursor', initialOffset = 0, enabled = true, queryKey = '', subscribe,
    invalidateDelayMs = EVENT_REFETCH_DEBOUNCE_MS } = options
  const serialized = queryJSON(options.params)
  const params = useMemo(() => JSON.parse(serialized) as P, [serialized])
  const latest = useRef(options)
  useEffect(() => { latest.current = options })
  const [state, setState] = useState<ListState<T>>({ items: [], loading: false, loadingMore: false, error: null, isStale: false })
  const current = useRef(state)
  const generation = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const rowControllers = useRef(new Set<AbortController>())
  const runningRows = useRef(new Set<string | number>())
  const rowRevisions = useRef(new Map<string | number, number>())
  const rowQueue = useRef(new Set<string | number>())
  const rowDebounce = useRef<ReturnType<typeof createTrailingDebounce> | null>(null)
  const invalidation = useRef<ReturnType<typeof createTrailingDebounce> | null>(null)
  const commit = useCallback((next: ListState<T>) => {
    current.current = next
    setState(next)
  }, [])

  useEffect(() => {
    const debounce = createTrailingDebounce(() => latest.current.onInvalidate?.(), invalidateDelayMs)
    invalidation.current = debounce
    return () => { debounce.cancel(); invalidation.current = null }
  }, [invalidateDelayMs])

  const request = useCallback(async (position: PagePosition, append: boolean, gen: number) => {
    const abort = new AbortController()
    controller.current = abort
    commit({ ...current.current, loading: true, loadingMore: append, error: null })
    try {
      const page = await latest.current.fetchPage({ ...position, params, signal: abort.signal })
      if (abort.signal.aborted || gen !== generation.current) return
      if (page.meta.has_more) {
        if (!page.items.length) throw new Error('Page reported more rows without returning progress.')
        if (mode === 'cursor' && (!page.meta.next_cursor || page.meta.next_cursor === position.cursor)) {
          throw new Error('Page did not provide a forward cursor.')
        }
        if (mode === 'offset' && (!Number.isSafeInteger(page.meta.next_offset) || (page.meta.next_offset ?? -1) <= (position.offset ?? 0))) {
          throw new Error('Page did not provide a forward offset.')
        }
      }
      const items = append ? [...current.current.items] : []
      const seen = new Set(items.map(latest.current.getId))
      for (const item of page.items) {
        const id = latest.current.getId(item)
        if (!seen.has(id)) { items.push(item); seen.add(id) }
      }
      commit({ items, meta: { ...page.meta, total: page.meta.total ?? current.current.meta?.total }, loading: false, loadingMore: false, error: null, isStale: current.current.isStale })
    } catch (error) {
      if (abort.signal.aborted || gen !== generation.current) return
      commit({ ...current.current, loading: false, loadingMore: false, error: error instanceof Error ? error : new Error(String(error)) })
    } finally {
      if (controller.current === abort) controller.current = null
    }
  }, [commit, mode, params])

  const reload = useCallback(async () => {
    const gen = ++generation.current
    controller.current?.abort()
    invalidation.current?.cancel()
    rowDebounce.current?.cancel()
    rowQueue.current.clear()
    runningRows.current.clear()
    rowRevisions.current.clear()
    rowControllers.current.forEach(abort => abort.abort())
    rowControllers.current.clear()
    commit({ items: [], loading: false, loadingMore: false, error: null, isStale: false })
    if (!enabled) return
    if (mode === 'offset' && (!Number.isSafeInteger(initialOffset) || initialOffset < 0)) {
      commit({ items: [], loading: false, loadingMore: false, error: new Error('Offset must be a non-negative safe integer.'), isStale: false })
      return
    }
    await request(mode === 'offset' ? { offset: initialOffset } : {}, false, gen)
  }, [commit, enabled, initialOffset, mode, request])

  useEffect(() => {
    void reload()
    return () => {
      ++generation.current
      controller.current?.abort()
      invalidation.current?.cancel()
      rowDebounce.current?.cancel()
      rowQueue.current.clear()
      runningRows.current.clear()
      rowRevisions.current.clear()
      rowControllers.current.forEach(abort => abort.abort())
      rowControllers.current.clear()
    }
  }, [reload, queryKey])

  const loadMore = useCallback(async () => {
    const { meta, loading } = current.current
    if (!enabled || loading || !meta?.has_more) return
    const position: PagePosition = mode === 'offset'
      ? { offset: meta.next_offset! }
      : { cursor: meta.next_cursor! }
    await request(position, true, generation.current)
  }, [enabled, mode, request])

  const patchRow = useCallback((change: ListChange<T>) => {
    const before = current.current
    const index = before.items.findIndex(item => latest.current.getId(item) === change.id)
    if (index < 0) return false
    const items = [...before.items]
    const item = change.item ?? { ...items[index], ...change.patch }
    if (change.remove || (latest.current.matches && !latest.current.matches(item))) items.splice(index, 1)
    else if (latest.current.getId(item) === change.id) items[index] = item
    commit({ ...before, items })
    return true
  }, [commit])

  const { rowRefreshDelayMs = 200, rowRefreshLimit = 20, rowRefreshConcurrency = 4 } = options
  useEffect(() => {
    const debounce = createTrailingDebounce(() => {
      if (rowControllers.current.size) { rowDebounce.current?.schedule(); return }
      const ids = [...rowQueue.current]
      rowQueue.current.clear()
      const gen = generation.current
      // The queue is capped before dispatch; workers process only these loaded IDs.
      let next = 0
      const worker = async (): Promise<void> => {
        const id = ids[next++]
        if (id === undefined || gen !== generation.current || current.current.isStale) return
        if (!current.current.items.some(item => latest.current.getId(item) === id)) return worker()
        const fetchItem = latest.current.fetchItem
        if (!fetchItem) return
        const abort = new AbortController()
        rowControllers.current.add(abort)
        runningRows.current.add(id)
        const revision = rowRevisions.current.get(id)
        try {
          const item = await fetchItem(id, { params, signal: abort.signal })
          if (!abort.signal.aborted && gen === generation.current && revision === rowRevisions.current.get(id)) patchRow({ id, item: item ?? undefined, remove: item === null })
        } catch {
          if (!abort.signal.aborted && gen === generation.current) commit({ ...current.current, isStale: true })
        } finally {
          rowControllers.current.delete(abort)
          if (gen === generation.current) runningRows.current.delete(id)
        }
        return worker()
      }
      const concurrency = Math.max(1, Math.min(4, Math.floor(rowRefreshConcurrency) || 1))
      void Promise.all(Array.from({ length: Math.min(concurrency, ids.length) }, worker))
    }, rowRefreshDelayMs)
    rowDebounce.current = debounce
    return () => { debounce.cancel(); rowDebounce.current = null }
  }, [commit, params, patchRow, rowRefreshConcurrency, rowRefreshDelayMs])

  const applyEvent = useCallback((change: ListChange<T>) => {
    const visible = current.current.items.some(item => latest.current.getId(item) === change.id)
    if (visible) {
      rowRevisions.current.set(change.id, (rowRevisions.current.get(change.id) ?? 0) + 1)
      if (change.remove || change.item || change.patch) {
        rowQueue.current.delete(change.id)
        patchRow(change)
      }
      if (change.refresh && !change.remove && !current.current.isStale) {
        if (!latest.current.fetchItem) commit({ ...current.current, isStale: true })
        else {
          rowQueue.current.add(change.id)
          if (new Set([...rowQueue.current, ...runningRows.current]).size > Math.max(1, rowRefreshLimit)) {
            rowQueue.current.clear()
            rowDebounce.current?.cancel()
            rowControllers.current.forEach(abort => abort.abort())
            commit({ ...current.current, isStale: true })
          } else rowDebounce.current?.schedule()
        }
      }
    }
    invalidation.current?.schedule()
  }, [commit, patchRow, rowRefreshLimit])

  useEffect(() => subscribe?.(applyEvent), [subscribe, applyEvent])

  return { ...state, total: state.meta?.total, hasMore: state.meta?.has_more ?? false,
    loadMore, reload, refresh: reload, applyEvent }
}
