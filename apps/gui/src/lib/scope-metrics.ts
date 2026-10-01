import type { Task, TaskScopeKey, TaskScopeRollupResponse, TaskStatus } from '@/lib/types'

export interface ScopeTaskRollup {
  total: number
  done: number
  open: number
  doing: number
  review: number
  blocked: number
  paused: number
  completion: number
}

/**
 * Buckets a scope's per-status counts. Archived, abandoned and cancelled
 * tasks are left out of every bucket and of total.
 */
export function rollupFromStatusCounts(counts: Readonly<Record<string, number>>): ScopeTaskRollup {
  const n = (status: TaskStatus) => counts[status] ?? 0
  const done = n('done')
  const open = n('backlog') + n('todo') + n('queued')
  const doing = n('doing')
  const review = n('review')
  const blocked = n('blocked')
  const paused = n('paused')
  const total = open + doing + review + blocked + paused + done

  return {
    total,
    done,
    open,
    doing,
    review,
    blocked,
    paused,
    completion: total > 0 ? Math.round((done / total) * 100) : 0,
  }
}

export function buildTaskRollup(tasks: ReadonlyArray<Pick<Task, 'status'>>): ScopeTaskRollup {
  const counts: Record<string, number> = {}
  for (const task of tasks) counts[task.status] = (counts[task.status] ?? 0) + 1
  return rollupFromStatusCounts(counts)
}

/** Rollups from GET /tasks/rollup, keyed by scope id. */
export function rollupsByScope(res: TaskScopeRollupResponse | null): Map<string, ScopeTaskRollup> {
  return new Map((res?.scopes ?? []).map((scope) => [scope.scope_id, rollupFromStatusCounts(scope.counts)]))
}

export function groupTasksByScope<T extends Pick<Task, TaskScopeKey>>(tasks: T[], key: TaskScopeKey): Map<string, T[]> {
  const groups = new Map<string, T[]>()
  for (const task of tasks) {
    const id = task[key]
    if (!id) continue
    const bucket = groups.get(id)
    if (bucket) bucket.push(task)
    else groups.set(id, [task])
  }
  return groups
}
