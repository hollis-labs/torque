import { describe, expect, it } from 'vitest'
import { buildTaskRollup, rollupFromStatusCounts, rollupsByScope } from './scope-metrics'
import type { TaskStatus } from './types'

describe('rollupFromStatusCounts', () => {
  it('buckets server counts the way buildTaskRollup buckets tasks', () => {
    const statuses: TaskStatus[] = ['backlog', 'todo', 'todo', 'queued', 'doing', 'review', 'blocked', 'paused', 'done', 'done', 'archived', 'cancelled']
    const counts: Record<string, number> = {}
    for (const s of statuses) counts[s] = (counts[s] ?? 0) + 1

    const fromCounts = rollupFromStatusCounts(counts)
    expect(fromCounts).toEqual(buildTaskRollup(statuses.map((status) => ({ status }))))
    expect(fromCounts).toEqual({ total: 10, done: 2, open: 4, doing: 1, review: 1, blocked: 1, paused: 1, completion: 20 })
  })

  it('is all zeroes for a scope with no tasks', () => {
    expect(rollupFromStatusCounts({})).toEqual({ total: 0, done: 0, open: 0, doing: 0, review: 0, blocked: 0, paused: 0, completion: 0 })
  })
})

describe('rollupsByScope', () => {
  it('keys each scope rollup by scope id', () => {
    const byScope = rollupsByScope({
      group_by: 'project_id',
      total: 3,
      scopes: [
        { scope_id: 'PRJ-A', total: 2, counts: { done: 1, todo: 1 } },
        { scope_id: 'PRJ-B', total: 1, counts: { blocked: 1 } },
      ],
    })
    expect(byScope.get('PRJ-A')?.completion).toBe(50)
    expect(byScope.get('PRJ-B')?.blocked).toBe(1)
    expect(rollupsByScope(null).size).toBe(0)
  })
})
