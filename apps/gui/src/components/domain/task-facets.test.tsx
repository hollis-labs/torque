import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { TaskFacets } from './task-facets'
import type { Task } from '@/lib/types'

function baseTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'T-1',
    title: 't',
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
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('TaskFacets', () => {
  it('renders core facets as chips', () => {
    const html = renderToStaticMarkup(<TaskFacets task={baseTask()} />)
    expect(html).toContain('Kind')
    expect(html).toContain('agent')
    expect(html).toContain('Source')
    expect(html).toContain('user')
    expect(html).toContain('Trust')
    expect(html).toContain('normal')
    expect(html).toContain('Checkpoint Mode')
    expect(html).toContain('On Checkpoint Response')
    expect(html).toContain('resume')
  })

  it('shows source_ref when present', () => {
    const html = renderToStaticMarkup(
      <TaskFacets task={baseTask({ source_ref: 'webhook-42' })} />,
    )
    expect(html).toContain('webhook-42')
  })

  it('omits source_ref span when empty', () => {
    const html = renderToStaticMarkup(<TaskFacets task={baseTask()} />)
    expect(html).not.toContain('font-mono text-zinc-400 break-all')
  })

  it('colors trust levels distinctively', () => {
    const trusted = renderToStaticMarkup(
      <TaskFacets task={baseTask({ trust: 'trusted' })} />,
    )
    expect(trusted).toContain('border-emerald-500/40')

    const untrusted = renderToStaticMarkup(
      <TaskFacets task={baseTask({ trust: 'untrusted' })} />,
    )
    expect(untrusted).toContain('border-red-500/40')
  })

  it('renders template_ref when metadata.template_ref is a well-formed object', () => {
    const html = renderToStaticMarkup(
      <TaskFacets
        task={baseTask({
          metadata: { template_ref: { id: 'tpl-smoke', version: 3 } },
        })}
      />,
    )
    expect(html).toContain('Template Ref')
    expect(html).toContain('tpl-smoke')
    expect(html).toContain('@v3')
  })

  it('hides template_ref when metadata is missing or malformed', () => {
    const missing = renderToStaticMarkup(<TaskFacets task={baseTask()} />)
    expect(missing).not.toContain('Template Ref')

    const malformed = renderToStaticMarkup(
      <TaskFacets task={baseTask({ metadata: { template_ref: 'bogus' } })} />,
    )
    expect(malformed).not.toContain('Template Ref')
  })
})
