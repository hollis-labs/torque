import type { Task, TaskStatus } from './types'

export const BLOCKED_REASON_TOOLTIP_LIMIT = 120

const STATUSES_WITH_REASON: TaskStatus[] = ['blocked', 'paused']

export function hasBlockedReason(task: Pick<Task, 'status' | 'blocked_reason'>): boolean {
  return (
    STATUSES_WITH_REASON.includes(task.status) &&
    task.blocked_reason.trim().length > 0
  )
}

export function truncateBlockedReason(
  reason: string,
  limit: number = BLOCKED_REASON_TOOLTIP_LIMIT,
): string {
  const trimmed = reason.trim()
  if (trimmed.length <= limit) return trimmed
  return `${trimmed.slice(0, limit).trimEnd()}…`
}
