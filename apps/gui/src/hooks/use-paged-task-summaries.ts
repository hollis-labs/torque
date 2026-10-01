import { useCallback } from 'react'
import { useApi } from './use-api'
import { usePagedList, type ListChange, type PageRequest } from './use-paged-list'
import type { TaskFilter, TaskSummary } from '@/lib/types'

export const SCOPE_TASK_PAGE_SIZE = 50
type ScopeFilter = Pick<TaskFilter, 'project_id' | 'epic_id' | 'sprint_id'>
type SummaryParams = Omit<TaskFilter, 'cursor' | 'offset'>

/** Scope task lists use bounded cursor pages and patch visible SSE statuses. */
export function usePagedTaskSummaries(scope: ScopeFilter, pageSize = SCOPE_TASK_PAGE_SIZE, onInvalidate?: () => void) {
  const api = useApi()
  const fetchPage = useCallback(({ params, cursor, signal }: PageRequest<SummaryParams>) =>
    api.listTaskSummaryPage({ ...params, cursor }, signal), [api])
  const subscribe = useCallback((onChange: (change: ListChange<TaskSummary>) => void) =>
    api.subscribeEvents(event => {
      if (!event.type.startsWith('task.')) return
      const id = event.data.task_id
      if (typeof id !== 'string') return
      onChange({ id, remove: event.type === 'task.deleted', refresh: event.type === 'task.updated',
        patch: event.type === 'task.transitioned' && typeof event.data.status === 'string'
          ? { status: event.data.status as TaskSummary['status'] } : undefined })
    }), [api])
  const fetchItem = useCallback((id: string | number, { signal }: { signal: AbortSignal }) =>
    api.getTask(String(id), signal).catch(error => {
      if (error instanceof Error && 'status' in error && error.status === 404) return null
      throw error
    }), [api])
  const page = usePagedList({ fetchPage, params: { ...scope, limit: pageSize,
    sort_by: 'updated_at', sort_dir: 'desc' } as SummaryParams,
    getId: (task: TaskSummary) => task.id, subscribe, onInvalidate, fetchItem,
    matches: (task: TaskSummary) => (!scope.project_id || task.project_id === scope.project_id)
      && (!scope.epic_id || task.epic_id === scope.epic_id)
      && (!scope.sprint_id || task.sprint_id === scope.sprint_id) })
  return { ...page, tasks: page.items }
}
