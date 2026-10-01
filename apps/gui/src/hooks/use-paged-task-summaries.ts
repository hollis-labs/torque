import { useCallback, useEffect, useRef, useState } from 'react'
import { useApi } from './use-api'
import type { TaskFilter, TaskSummary } from '@/lib/types'

export const SCOPE_TASK_PAGE_SIZE = 50

type ScopeFilter = Pick<TaskFilter, 'project_id' | 'epic_id' | 'sprint_id'>

export interface PagedTaskSummaries {
  tasks: TaskSummary[]
  total: number
  hasMore: boolean
  loadingMore: boolean
  /** Refetches from the top, keeping as many rows as are already shown. */
  reload: () => Promise<void>
  loadMore: () => Promise<void>
}

/**
 * One scope's task list a page at a time, newest update first, using the
 * description-less summary projection. Scope detail pages used to fetch
 * every task in scope up front (CW-20261001-0023).
 */
export function usePagedTaskSummaries(scope: ScopeFilter, pageSize = SCOPE_TASK_PAGE_SIZE): PagedTaskSummaries {
  const api = useApi()
  const [tasks, setTasks] = useState<TaskSummary[]>([])
  const [total, setTotal] = useState(0)
  const [loadingMore, setLoadingMore] = useState(false)
  const generation = useRef(0)
  const shown = useRef(0)
  useEffect(() => {
    shown.current = tasks.length
  }, [tasks])
  const { project_id, epic_id, sprint_id } = scope

  const fetchPage = useCallback(
    (offset: number, limit: number) =>
      api.listTaskSummaries({ project_id, epic_id, sprint_id, sort_by: 'updated_at', sort_dir: 'desc', offset, limit }),
    [api, project_id, epic_id, sprint_id]
  )

  const reload = useCallback(async () => {
    const gen = ++generation.current
    const res = await fetchPage(0, Math.max(pageSize, shown.current))
    if (gen !== generation.current) return
    setTasks(res.tasks)
    setTotal(res.total)
  }, [fetchPage, pageSize])

  const loadMore = useCallback(async () => {
    const gen = ++generation.current
    setLoadingMore(true)
    try {
      const res = await fetchPage(shown.current, pageSize)
      if (gen !== generation.current) return
      // Offset paging can repeat a row when tasks move between requests.
      setTasks((prev) => {
        const seen = new Set(prev.map((t) => t.id))
        return [...prev, ...res.tasks.filter((t) => !seen.has(t.id))]
      })
      setTotal(res.total)
    } finally {
      setLoadingMore(false)
    }
  }, [fetchPage, pageSize])

  return { tasks, total, hasMore: tasks.length < total, loadingMore, reload, loadMore }
}
