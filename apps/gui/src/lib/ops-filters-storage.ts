import type { TaskStatus } from './types'

const KEY = 'clockwork:ops:filters:v1'

/**
 * Manual-flag filter tri-state.
 * - `both`   = no filter (default)
 * - `auto`   = manual=false (scheduler-eligible tasks)
 * - `manual` = manual=true (held for review before dispatch)
 */
export type ManualFilter = 'both' | 'auto' | 'manual'

const MANUAL_VALUES: readonly ManualFilter[] = ['both', 'auto', 'manual'] as const

export function parseManualFilter(raw: unknown): ManualFilter {
  if (typeof raw !== 'string') return 'both'
  // Legacy migration: older blobs stored the default as 'all'.
  if (raw === 'all') return 'both'
  return (MANUAL_VALUES as readonly string[]).includes(raw) ? (raw as ManualFilter) : 'both'
}

export interface OpsFilters {
  statuses: TaskStatus[]
  priorities: number[]
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  manual: ManualFilter
  /** Free-text search over title + description. */
  search: string
}

export function saveOpsFilters(filters: OpsFilters): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(filters))
  } catch {
    // localStorage may be unavailable (private mode / quota) — fail quiet
  }
}

export function readOpsFilters(): OpsFilters | null {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return null
    const f = parsed as Record<string, unknown>
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
  } catch {
    return null
  }
}

export function clearOpsFilters(): void {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // ignore
  }
}
