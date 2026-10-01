import { describe, expect, it } from 'vitest'
import { patchTaskStatus, taskIdFromEvent, transitionedStatus, upsertTasks } from './dashboard-events'
import type { TaskSummary } from './types'

const row = (id: string, status: TaskSummary['status'] = 'todo') => ({ id, status, updated_at: 'then' }) as TaskSummary

describe('transitionedStatus', () => {
  it('reads the HTTP shape and the scheduler-bridge shape', () => {
    expect(transitionedStatus({ type: 'task.transitioned', data: { task_id: 'T-1', status: 'done' } })).toBe('done')
    expect(transitionedStatus({ type: 'task.transitioned', data: { task_id: 'T-1', payload: { from: 'doing', to: 'review' } } })).toBe('review')
  })

  it('is null for other events, missing statuses and unknown statuses', () => {
    expect(transitionedStatus({ type: 'task.updated', data: { task_id: 'T-1', status: 'done' } })).toBeNull()
    expect(transitionedStatus({ type: 'task.transitioned', data: { task_id: 'T-1' } })).toBeNull()
    expect(transitionedStatus({ type: 'task.transitioned', data: { task_id: 'T-1', status: 'in_progress' } })).toBeNull()
  })
})

describe('taskIdFromEvent', () => {
  it('returns the task id or null', () => {
    expect(taskIdFromEvent({ type: 'task.created', data: { task_id: 'T-9', title: 'x' } })).toBe('T-9')
    expect(taskIdFromEvent({ type: 'task.created', data: {} })).toBeNull()
  })
})

describe('patchTaskStatus', () => {
  it('patches the matching row and leaves the array alone otherwise', () => {
    const tasks = [row('T-1'), row('T-2')]
    const out = patchTaskStatus(tasks, 'T-2', 'done', 'now')
    expect(out[1]).toMatchObject({ id: 'T-2', status: 'done', updated_at: 'now' })
    expect(out[0]).toBe(tasks[0])
    expect(patchTaskStatus(tasks, 'T-404', 'done', 'now')).toBe(tasks)
    expect(patchTaskStatus(tasks, 'T-1', 'todo', 'now')).toBe(tasks)
  })
})

describe('upsertTasks', () => {
  it('replaces rows in place, prepends new ones and keeps the cap', () => {
    const tasks = [row('T-1'), row('T-2'), row('T-3')]
    const out = upsertTasks(tasks, [row('T-2', 'blocked'), row('T-9')], 3)
    expect(out.map((t) => `${t.id}:${t.status}`)).toEqual(['T-9:todo', 'T-1:todo', 'T-2:blocked'])
  })
})
