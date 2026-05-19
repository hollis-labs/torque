import { createListCursor } from '@hollis-labs/sysop-ui'
import type { TaskStatus } from './types'
import { parseManualFilter, type ManualFilter } from './ops-filters-storage'

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

// Session-scoped task-list cursor — the kit's `createListCursor` owns the
// session-storage mechanics; Torque keeps the `CursorFilter` shape + its
// validator local.
const cursor = createListCursor<CursorFilter>('torque:task-list-cursor')

function parseFilter(raw: unknown): CursorFilter | null {
  if (!raw || typeof raw !== 'object') return null
  const f = raw as Record<string, unknown>
  const statuses = Array.isArray(f.statuses)
    ? ((f.statuses as unknown[]).filter((s): s is TaskStatus => typeof s === 'string') as TaskStatus[])
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

export function saveTaskListCursor(ids: string[], filter: CursorFilter | null = null): void {
  cursor.save(ids, filter)
}

export function readTaskListCursor(): TaskListCursor {
  const stored = cursor.read()
  return { ids: stored.ids, filter: parseFilter(stored.filter) }
}

export function clearTaskListCursor(): void {
  cursor.clear()
}
