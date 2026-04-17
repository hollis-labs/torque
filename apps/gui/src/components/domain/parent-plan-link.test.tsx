import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { ApiProvider } from '@/hooks/use-api'
import { ParentPlanLink } from './parent-plan-link'
import type { Task } from '@/lib/types'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'CW-CHILD-1',
    title: 'child',
    description: '',
    status: 'todo',
    priority: 2,
    tags: [],
    manual: true,
    executor: 'cli',
    agent_profile: '',
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
    on_done: 'review',
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
    created_at: '2026-04-17T00:00:00Z',
    updated_at: '2026-04-17T00:00:00Z',
    ...overrides,
  }
}

describe('ParentPlanLink', () => {
  it('renders nothing when parent_id is null', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ApiProvider baseUrl="/api/v1">
          <ParentPlanLink task={makeTask({ parent_id: null })} />
        </ApiProvider>
      </MemoryRouter>,
    )
    // Static render captures only the initial paint — the effect hasn't
    // run, so even if parent_id were set we would not see the banner.
    // We assert the null-parent path renders an empty string so React
    // doesn't emit the frame at all.
    expect(html).toBe('')
  })

  it('renders nothing when parent_id is undefined on the task', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ApiProvider baseUrl="/api/v1">
          <ParentPlanLink task={makeTask({})} />
        </ApiProvider>
      </MemoryRouter>,
    )
    expect(html).toBe('')
  })
})
