import type { TaskStatus } from './types'

const KEY = 'clockwork:ops:filters:v1'

export interface OpsFilters {
  statuses: TaskStatus[]
  priorities: number[]
  projectId: string | null
  sprintId: string | null
  epicId: string | null
  tagSlug: string | null
  mode: string
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
      mode: typeof f.mode === 'string' ? f.mode : 'all',
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
