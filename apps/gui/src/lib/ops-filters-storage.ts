import { createScopedStorage } from '@hollis-labs/sysop-ui'
import type { TaskStatus } from './types'

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
  /**
   * Surface kind=internal automation tasks (Reviewer end-agents etc.) in
   * the list view. Default false; the user opts in via the FilterBar's
   * System toggle (CW-20260503-0011).
   */
  includeInternal: boolean
}

/** Validates / sanitizes a raw stored blob into a well-formed `OpsFilters`. */
function parseOpsFilters(raw: unknown): OpsFilters | null {
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
    // Default false on parse so legacy blobs (pre-CW-20260503-0011)
    // surface internal=hidden, matching the backend default.
    includeInternal: f.includeInternal === true,
  }
}

// localStorage-backed persistence — the kit's `createScopedStorage` owns the
// fail-quiet web-storage mechanics; Torque keeps the `OpsFilters` shape +
// its `parse` validator local.
const storage = createScopedStorage<OpsFilters>('torque:ops:filters:v1', {
  area: 'local',
  parse: parseOpsFilters,
})

export function saveOpsFilters(filters: OpsFilters): void {
  storage.write(filters)
}

export function readOpsFilters(): OpsFilters | null {
  return storage.read()
}

export function clearOpsFilters(): void {
  storage.clear()
}
