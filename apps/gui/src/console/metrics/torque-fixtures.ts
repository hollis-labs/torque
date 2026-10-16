// Realistic Torque-shaped fixtures + an in-memory TorqueMetricsClient for tests and previews.
import type { Epic, Sprint, TaskSummary } from '@/lib/types'
import type { ConsoleEvent } from '../events/types'
import { KINDS } from '../events/types'
import type { TorqueMetricsClient } from './torque-source'

const DAY = 86_400_000
export const TORQUE_NOW = Date.parse('2026-10-16T12:00:00Z')
const iso = (t: number) => new Date(t).toISOString()

function task(i: number, status: string, createdDaysAgo: number, updatedDaysAgo: number): TaskSummary {
  return { id: `CW-T${i}`, title: `Task ${i}`, status, created_at: iso(TORQUE_NOW - createdDaysAgo * DAY), updated_at: iso(TORQUE_NOW - updatedDaysAgo * DAY), epic_id: 'EP-1', sprint_id: 'SP-1', tags: [] } as unknown as TaskSummary
}

/** 16 tasks on a sprint that started 9 days ago: 8 done, 2 review, 2 doing, 1 blocked, 3 todo; 3 created mid-sprint; 1 cancelled. */
export function sprintTasks(): TaskSummary[] {
  return [
    task(1, 'done', 10, 8), task(2, 'done', 10, 7), task(3, 'done', 10, 6), task(4, 'done', 10, 5), task(5, 'done', 10, 4),
    task(6, 'done', 10, 2), task(7, 'done', 6, 1.5), task(8, 'done', 6, 0.5),
    task(9, 'review', 10, 0.3), task(10, 'review', 6, 0.2), task(11, 'doing', 10, 0.1), task(12, 'doing', 3, 0.1),
    task(13, 'blocked', 10, 2), task(14, 'todo', 10, 9), task(15, 'todo', 3, 3), task(16, 'backlog', 2, 2), task(17, 'cancelled', 10, 5),
  ]
}

export const FIXTURE_SPRINT: Sprint = {
  id: 'SP-1', name: 'Sprint 1', goal: 'Ship', status: 'active', approval_mode: 'manual', cost_budget: null, project_id: 'PRJ-1', epic_id: 'EP-1',
  started_at: iso(TORQUE_NOW - 9 * DAY), ended_at: iso(TORQUE_NOW + 5 * DAY), created_at: iso(TORQUE_NOW - 12 * DAY), updated_at: iso(TORQUE_NOW),
}
export const FIXTURE_EPIC: Epic = { id: 'EP-1', name: 'Epic 1', description: '', status: 'active', priority: 3, project_id: 'PRJ-1', created_at: iso(TORQUE_NOW - 12 * DAY), updated_at: iso(TORQUE_NOW) }

export interface FakeClientOptions { tasks?: TaskSummary[]; sprint?: Sprint; epic?: Epic; failWith?: string; pageSize?: number }

export function fakeTorqueClient(o: FakeClientOptions = {}): TorqueMetricsClient & { calls: string[] } {
  const tasks = o.tasks ?? sprintTasks()
  const calls: string[] = []
  const fail = () => { if (o.failWith) throw new Error(o.failWith) }
  return {
    calls,
    async taskRollup(groupBy, filter) {
      calls.push(`rollup:${groupBy}`)
      fail()
      const counts: Record<string, number> = {}
      for (const t of tasks) counts[t.status] = (counts[t.status] ?? 0) + 1
      const id = (filter?.epic_id ?? filter?.sprint_id ?? filter?.project_id) as string
      return { group_by: groupBy, total: tasks.length, scopes: [{ scope_id: id, total: tasks.length, counts }] }
    },
    async listTaskSummaries(filter) {
      calls.push('list')
      fail()
      const size = o.pageSize ?? filter?.limit ?? 200
      const start = filter?.cursor ? Number(filter.cursor) : 0
      const items = tasks.slice(start, start + size)
      const more = start + size < tasks.length
      return { items, meta: { returned: items.length, limit: size, has_more: more, next_cursor: more ? String(start + size) : null } }
    },
    async getSprint() { calls.push('sprint'); fail(); return o.sprint ?? FIXTURE_SPRINT },
    async getEpic() { calls.push('epic'); fail(); return o.epic ?? FIXTURE_EPIC },
  }
}

/** agent.state + metric.tick events over the last hour for the fleet views. */
export function fleetEvents(now = TORQUE_NOW, withTicks = true): ConsoleEvent[] {
  const mk = (id: string, minAgo: number, agent: string, state: string): ConsoleEvent => ({
    id, ts: now - minAgo * 60_000, kind: KINDS.agentState, source: { id: 'fixture' }, subjects: [{ type: 'agent', id: agent }], severity: 'info',
    payload: { state }, actions: [], extra: {},
  })
  const ev = [
    mk('s1', 55, 'alpha', 'idle'), mk('s2', 50, 'beta', 'busy'), mk('s3', 40, 'alpha', 'busy'), mk('s4', 30, 'gamma', 'waiting'),
    mk('s5', 20, 'beta', 'idle'), mk('s6', 10, 'gamma', 'busy'), mk('s7', 5, 'delta', 'busy'),
  ]
  if (withTicks) {
    for (let i = 0; i < 6; i++) ev.push({ id: `k${i}`, ts: now - (50 - i * 10) * 60_000, kind: KINDS.metricTick, source: { id: 'fixture' }, subjects: [], severity: 'debug', payload: { metric: 'tokens_per_min', value: 6000 + i * 500 }, actions: [], extra: {} })
  }
  return ev
}
