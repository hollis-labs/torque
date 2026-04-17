import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ActivityHeatmap, RunsChart, TaskPipeline } from './index'
import type { Run, Task } from '@/lib/types'

function makeTask(over: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'test',
    description: '',
    status: 'doing',
    priority: 2,
    tags: [],
    manual: false,
    executor: 'cli',
    agent_profile: '',
    working_dir: '',
    kind: 'agent',
    source_type: 'user',
    source_ref: null,
    trust: 'normal',
    checkpoint_mode: 'none',
    on_checkpoint_response: 'resume',
    deliverables: [],
    deliverable_preset: '',
    on_done: 'review',
    on_fail: 'retry',
    on_review: 'pause',
    on_done_merge: 'none',
    escalation_chain: [],
    quality_gates: [],
    blocked_reason: '',
    depends_on: [],
    project_id: null,
    sprint_id: null,
    epic_id: null,
    updated_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    cost_budget: -1,
    max_retries: 3,
    max_duration_ms: -1,
    token_budget: -1,
    files: [],
    tools: [],
    permissions: {},
    environment: {},
    system_prompt: '',
    agent_file: '',
    metadata: {},
    ...over,
  } as Task
}

function makeRun(over: Partial<Run> = {}): Run {
  return {
    id: 1,
    task_id: 't1',
    executor: 'cli',
    agent_profile: '',
    status: 'success',
    prompt_tokens: 0,
    completion_tokens: 0,
    cost: 0,
    exit_code: 0,
    error_message: '',
    started_at: new Date().toISOString(),
    completed_at: null,
    ...over,
  }
}

describe('ActivityHeatmap', () => {
  it('renders with empty data', () => {
    const html = renderToStaticMarkup(<ActivityHeatmap tasks={[]} runs={[]} />)
    expect(html).toContain('total events')
  })

  it('renders with populated data', () => {
    const html = renderToStaticMarkup(
      <ActivityHeatmap tasks={[makeTask()]} runs={[makeRun()]} weekCount={8} />
    )
    expect(html).toContain('total events')
  })
})

describe('RunsChart', () => {
  it('renders the empty state when runs list is empty', () => {
    const html = renderToStaticMarkup(<RunsChart runs={[]} />)
    expect(html).toContain('No runs in window')
  })

  it('does not crash with populated runs', () => {
    const html = renderToStaticMarkup(
      <RunsChart runs={[makeRun(), makeRun({ id: 2, status: 'error' })]} />
    )
    expect(html).toContain('Runs')
  })
})

describe('TaskPipeline', () => {
  it('renders with empty tasks', () => {
    const html = renderToStaticMarkup(<TaskPipeline tasks={[]} />)
    expect(html).toContain('Task Pipeline')
    expect(html).toContain('0 total')
  })

  it('counts tasks by status', () => {
    const html = renderToStaticMarkup(
      <TaskPipeline tasks={[makeTask({ status: 'doing' }), makeTask({ status: 'done' })]} />
    )
    expect(html).toContain('2 total')
  })
})
