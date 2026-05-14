import type { TaskStatus } from './types'
import { parseManualFilter, type ManualFilter } from './ops-filters-storage'

const KEY = 'torque:task-list-cursor'

export interface CursorFilter {
  statuses: TaskStatus[]
  priorities: number[]
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  manual: ManualFilter
  search: string
}

export interface TaskListCursor {
  ids: string[]
  filter: CursorFilter | null
}

export function saveTaskListCursor(ids: string[], filter: CursorFilter | null = null): void {
  try {
    const payload: TaskListCursor = { ids, filter }
    sessionStorage.setItem(KEY, JSON.stringify(payload))
  } catch {
    // sessionStorage may be unavailable (private mode / quota) — fail quiet
  }
}

export function readTaskListCursor(): TaskListCursor {
  const empty: TaskListCursor = { ids: [], filter: null }
  try {
    const raw = sessionStorage.getItem(KEY)
    if (!raw) return empty
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return empty
    const obj = parsed as Record<string, unknown>
    const ids = Array.isArray(obj.ids)
      ? (obj.ids as unknown[]).filter((v): v is string => typeof v === 'string')
      : []
    const filter = parseFilter(obj.filter)
    return { ids, filter }
  } catch {
    return empty
  }
}

export function clearTaskListCursor(): void {
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // ignore
  }
}

function parseFilter(raw: unknown): CursorFilter | null {
  if (!raw || typeof raw !== 'object') return null
  const f = raw as Record<string, unknown>
  const statuses = Array.isArray(f.statuses)
    ? (f.statuses as unknown[]).filter((s): s is TaskStatus => typeof s === 'string') as TaskStatus[]
    : []
  const priorities = Array.isArray(f.priorities)
    ? (f.priorities as unknown[]).filter((p): p is number => typeof p === 'number')
    : []
  return {
    statuses,
    priorities,
    projectId: typeof f.projectId === 'string' ? f.projectId : null,
    sprintId: typeof f.sprintId === 'string' ? f.sprintId : null,
    epicId: typeof f.epicId === 'string' ? f.epicId : null,
    tagSlug: typeof f.tagSlug === 'string' ? f.tagSlug : null,
    manual: parseManualFilter(f.manual),
    search: typeof f.search === 'string' ? f.search : '',
  }
}
