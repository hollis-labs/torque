import { describe, it, expect } from 'vitest'
import { computeTaskDiff } from './task-diff'
import type { Task, Tag } from './types'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'tsk_test',
    title: 'Original title',
    description: 'Original description',
    status: 'todo',
    priority: 2,
    tags: [],
    manual: false,
    executor: 'cli',
    agent_profile: 'default',
    working_dir: '',
    kind: 'agent',
    source_type: 'user',
    source_ref: null,
    trust: 'normal',
    checkpoint_mode: 'none',
    on_checkpoint_response: 'resume',
    tools: [],
    permissions: {},
    environment: {},
    system_prompt: '',
    files: [],
    cost_budget: null,
    max_retries: 3,
    max_duration_ms: null,
    token_budget: null,
    on_done: 'close',
    on_fail: 'retry',
    on_review: 'pause',
    on_done_merge: 'none',
    escalation_chain: [],
    quality_gates: [],
    deliverables: [],
    deliverable_preset: '',
    depends_on: [],
    blocked_reason: '',
    metadata: {},
    sprint_id: null,
    project_id: null,
    epic_id: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeTag(slug: string, name?: string): Tag {
  return {
    slug,
    name: name ?? slug,
    description: '',
    color: 'zinc',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

describe('computeTaskDiff', () => {
  it('returns empty object when nothing changed', () => {
    const task = makeTask()
    const diff = computeTaskDiff(task, task)
    expect(diff).toEqual({})
  })

  it('returns only fields that changed', () => {
    const original = makeTask({ title: 'Original', priority: 2 })
    const draft = { ...original, title: 'Updated' }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ title: 'Updated' })
  })

  it('maps tags from Tag[] to string[] of tag names', () => {
    const original = makeTask({ tags: [makeTag('bug')] })
    const draft = { ...original, tags: [makeTag('bug'), makeTag('urgent', 'Urgent')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tags: ['bug', 'Urgent'] })
  })

  it('detects no tag change when tag slugs match', () => {
    const original = makeTask({ tags: [makeTag('bug'), makeTag('urgent')] })
    const draft = { ...original, tags: [makeTag('bug'), makeTag('urgent')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({})
  })

  it('detects tag removal', () => {
    const original = makeTask({ tags: [makeTag('bug'), makeTag('urgent')] })
    const draft = { ...original, tags: [makeTag('bug')] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tags: ['bug'] })
  })

  it('detects string array change (tools)', () => {
    const original = makeTask({ tools: ['bash', 'git'] })
    const draft = { ...original, tools: ['bash', 'git', 'grep'] }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ tools: ['bash', 'git', 'grep'] })
  })

  it('detects record change (environment)', () => {
    const original = makeTask({ environment: { NODE_ENV: 'test' } })
    const draft = { ...original, environment: { NODE_ENV: 'prod' } }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ environment: { NODE_ENV: 'prod' } })
  })

  it('detects nullable sentinel changes (cost_budget)', () => {
    const original = makeTask({ cost_budget: null })
    const draft = { ...original, cost_budget: -1 }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ cost_budget: -1 })
  })

  it('excludes id, created_at, updated_at, status from the diff', () => {
    const original = makeTask()
    const draft = {
      ...original,
      id: 'tsk_other',
      status: 'doing' as const,
      created_at: '2026-02-01T00:00:00Z',
      updated_at: '2026-02-01T00:00:00Z',
      title: 'Changed',
    }
    const diff = computeTaskDiff(original, draft)
    expect(diff).toEqual({ title: 'Changed' })
  })
})
