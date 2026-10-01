import { TASK_STATUSES } from '@/lib/constants'
import type { SSEEvent, TaskStatus, TaskSummary } from '@/lib/types'

/**
 * How the dashboard folds task events into the task rows it already holds
 * instead of refetching its whole sample on every event (CW-20261001-0023).
 */

export function taskIdFromEvent(ev: SSEEvent): string | null {
  return typeof ev.data.task_id === 'string' && ev.data.task_id ? ev.data.task_id : null
}

/**
 * The status a task.transitioned event moved its task to. HTTP transitions
 * send it as data.status and scheduler ones as data.payload.to; anything
 * outside the status vocabulary is treated as unknown.
 */
export function transitionedStatus(ev: SSEEvent): TaskStatus | null {
  if (ev.type !== 'task.transitioned') return null
  const payload = ev.data.payload && typeof ev.data.payload === 'object' ? (ev.data.payload as Record<string, unknown>) : {}
  const raw = typeof ev.data.status === 'string' ? ev.data.status : payload.to
  return typeof raw === 'string' && (TASK_STATUSES as readonly string[]).includes(raw) ? (raw as TaskStatus) : null
}

/** Sets one row's status in place; a task outside the sample is ignored. */
export function patchTaskStatus<T extends TaskSummary>(tasks: T[], id: string, status: TaskStatus, at: string): T[] {
  const idx = tasks.findIndex((t) => t.id === id)
  if (idx === -1 || tasks[idx].status === status) return tasks
  const next = tasks.slice()
  next[idx] = { ...next[idx], status, updated_at: at }
  return next
}

/**
 * Replaces refetched rows where they already are and puts new ones first,
 * keeping the sample at `cap` rows.
 */
export function upsertTasks<T extends TaskSummary>(tasks: T[], updates: T[], cap: number): T[] {
  if (updates.length === 0) return tasks
  const byId = new Map(updates.map((t) => [t.id, t]))
  const replaced = tasks.map((t) => {
    const u = byId.get(t.id)
    if (!u) return t
    byId.delete(t.id)
    return u
  })
  return [...byId.values(), ...replaced].slice(0, cap)
}
