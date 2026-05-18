import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  ActivityHeatmap,
  RunsChart,
  TaskPipeline,
  TokenThroughput,
  RunStatusDistribution,
  RecentRuns,
  Pulse24h,
  CostPerDay,
} from './index'
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

describe('TokenThroughput', () => {
  it('renders empty state when no tokens', () => {
    const html = renderToStaticMarkup(<TokenThroughput runs={[]} />)
    expect(html).toContain('No tokens in window')
  })

  it('renders totals when tokens present', () => {
    const html = renderToStaticMarkup(
      <TokenThroughput
        runs={[makeRun({ prompt_tokens: 1200, completion_tokens: 800 })]}
      />
    )
    expect(html).toContain('total tokens')
  })
})

describe('RunStatusDistribution', () => {
  it('renders empty state with no runs', () => {
    const html = renderToStaticMarkup(<RunStatusDistribution runs={[]} />)
    expect(html).toContain('No data recorded')
  })

  it('classifies statuses into buckets', () => {
    const html = renderToStaticMarkup(
      <RunStatusDistribution
        runs={[
          makeRun({ id: 1, status: 'success' }),
          makeRun({ id: 2, status: 'failed' }),
          makeRun({ id: 3, status: 'running' }),
        ]}
      />
    )
    expect(html).toContain('success')
    expect(html).toContain('error')
    expect(html).toContain('active')
    expect(html).toContain('3 runs')
  })
})

describe('RecentRuns', () => {
  it('renders empty state with no runs', () => {
    const html = renderToStaticMarkup(<RecentRuns runs={[]} />)
    expect(html).toContain('No runs yet')
  })

  it('limits to N newest runs', () => {
    const runs = Array.from({ length: 30 }).map((_, i) =>
      makeRun({
        id: i + 1,
        started_at: new Date(Date.now() - i * 60_000).toISOString(),
      })
    )
    const html = renderToStaticMarkup(<RecentRuns runs={runs} limit={5} />)
    expect(html).toContain('5 shown')
  })
})

describe('Pulse24h', () => {
  it('renders empty state with no recent runs', () => {
    const html = renderToStaticMarkup(<Pulse24h runs={[]} />)
    expect(html).toContain('No activity in last 24h')
  })

  it('counts runs in the last 24h window', () => {
    const runs = [
      makeRun({ id: 1, started_at: new Date(Date.now() - 30 * 60_000).toISOString() }),
      makeRun({ id: 2, started_at: new Date(Date.now() - 3 * 60 * 60_000).toISOString() }),
    ]
    const html = renderToStaticMarkup(<Pulse24h runs={runs} />)
    expect(html).toContain('2 ·')
  })
})

describe('CostPerDay', () => {
  it('renders empty state with no cost', () => {
    const html = renderToStaticMarkup(<CostPerDay runs={[]} />)
    expect(html).toContain('No cost recorded')
  })

  it('shows total cost when populated', () => {
    const today = new Date().toISOString()
    const html = renderToStaticMarkup(
      <CostPerDay runs={[makeRun({ cost: 1.25, started_at: today })]} />
    )
    expect(html).toContain('total')
  })
})
