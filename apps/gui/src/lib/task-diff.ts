import type { Task, Tag } from './types'

/**
 * Fields that are never sent in a task update PATCH body.
 * - id/created_at/updated_at: server-managed
 * - status: must use /transition endpoint, not PATCH
 * - tags: handled separately (Tag[] -> string[] mapping)
 */
const EXCLUDED_FIELDS = new Set([
  'id',
  'status',
  'created_at',
  'updated_at',
  'tags',
])

export type TaskUpdatePayload = Partial<Omit<Task, 'tags'>> & { tags?: string[] }

/**
 * Computes the minimal PATCH payload to send to PATCH /tasks/:id.
 *
 * Compares each field of `original` against `draft` using a deep-equality
 * check for arrays/objects and strict equality for primitives. Only fields
 * that differ are included in the result.
 *
 * Tags are a special case: the API accepts `string[]` of tag names, but the
 * Task type carries `Tag[]`. We compare by slug and map to names on change.
 */
export function computeTaskDiff(original: Task, draft: Task): TaskUpdatePayload {
  const diff: TaskUpdatePayload = {}

  for (const key of Object.keys(draft) as (keyof Task)[]) {
    if (EXCLUDED_FIELDS.has(key as string)) continue
    const a = original[key]
    const b = draft[key]
    if (!deepEqual(a, b)) {
      // Assign through unknown to satisfy strict typing — we've already
      // confirmed the key is not one of the excluded/special-cased fields.
      (diff as Record<string, unknown>)[key] = b
    }
  }

  if (!tagsEqual(original.tags, draft.tags)) {
    diff.tags = draft.tags.map((t) => t.name)
  }

  return diff
}

function tagsEqual(a: Tag[], b: Tag[]): boolean {
  if (a.length !== b.length) return false
  const aSlugs = a.map((t) => t.slug).sort()
  const bSlugs = b.map((t) => t.slug).sort()
  return aSlugs.every((s, i) => s === bSlugs[i])
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a === null || b === null) return false
  if (typeof a !== typeof b) return false
  if (typeof a !== 'object') return false
  if (Array.isArray(a) !== Array.isArray(b)) return false

  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false
    return a.every((v, i) => deepEqual(v, b[i]))
  }

  const aObj = a as Record<string, unknown>
  const bObj = b as Record<string, unknown>
  const aKeys = Object.keys(aObj)
  const bKeys = Object.keys(bObj)
  if (aKeys.length !== bKeys.length) return false
  return aKeys.every((k) => deepEqual(aObj[k], bObj[k]))
}
