// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ApiProvider } from '@/hooks/use-api'
import { buildHITLCheckpointPayload, findLikelyPullRequestUrl } from '@/lib/hitl-request'
import type { Artifact, Checkpoint, Task } from '@/lib/types'
import { TaskHITLRequestDialog } from './task-hitl-request-dialog'

vi.mock('@/lib/toast', () => ({
  notifyError: vi.fn(),
  notifySuccess: vi.fn(),
}))

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'CW-20260510-0120',
    title: 'Add HITL request action',
    description: 'Let users request checkpoints from task detail.',
    status: 'review',
    priority: 2,
    tags: [],
    manual: true,
    executor: 'cli',
    agent_profile: '',
    working_dir: '/repo',
    kind: 'agent',
    source_type: 'user',
    source_ref: null,
    trust: 'normal',
    checkpoint_mode: 'blocking',
    on_checkpoint_response: 'resume',
    tools: [],
    permissions: {},
    environment: {},
    system_prompt: '',
    files: ['apps/gui/src/pages/TaskDetailPage.tsx'],
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
    deliverables: [{ type: 'pr-link', required: false }],
    deliverable_preset: '',
    depends_on: [],
    blocked_reason: '',
    metadata: {},
    sprint_id: null,
    project_id: null,
    epic_id: null,
    created_at: '2026-05-11T00:00:00Z',
    updated_at: '2026-05-11T00:00:00Z',
    ...overrides,
  }
}

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 1,
    task_id: 'CW-20260510-0120',
    run_id: null,
    type: 'pr-link',
    content: '',
    url: 'https://github.com/hollis-labs/torque/pull/42',
    file_path: '',
    metadata: {},
    created_at: '2026-05-11T00:00:00Z',
    ...overrides,
  }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 201,
    headers: { 'Content-Type': 'application/json' },
  })
}

function emittedCheckpoint(): Checkpoint {
  return {
    id: 1,
    task_id: 'CW-20260510-0120',
    run_id: null,
    correlation_id: 'corr-1',
    type: 'pr_review',
    payload_json: '{}',
    response_json: null,
    emitter_source_type: 'user',
    emitter_source_ref: 'gui',
    responder_source_type: null,
    responder_source_ref: null,
    emitted_at: '2026-05-11T00:00:00Z',
    responded_at: null,
    timeout_at: null,
    status: 'pending',
  }
}

describe('TaskHITLRequestDialog helpers', () => {
  it('prefills pull request review payloads from task context and PR artifacts', () => {
    const task = makeTask()
    const artifact = makeArtifact()

    expect(findLikelyPullRequestUrl(task, [artifact])).toBe(artifact.url)
    expect(buildHITLCheckpointPayload('pr_review', task, [artifact])).toMatchObject({
      pr_url: artifact.url,
      title: task.title,
      summary: task.description,
      task_id: task.id,
    })
  })
})

describe('TaskHITLRequestDialog', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    globalThis.fetch = vi.fn(() =>
      Promise.resolve(jsonResponse(emittedCheckpoint())),
    ) as unknown as typeof fetch
  })

  afterEach(() => {
    cleanup()
  })

  it('emits a typed checkpoint with the editable prefilled payload', async () => {
    const onRequested = vi.fn()
    render(
      <ApiProvider>
        <TaskHITLRequestDialog
          task={makeTask()}
          artifacts={[makeArtifact()]}
          open
          onOpenChange={() => {}}
          onRequested={onRequested}
        />
      </ApiProvider>,
    )

    const payload = screen.getByLabelText('Payload JSON') as HTMLTextAreaElement
    const parsed = JSON.parse(payload.value)
    expect(parsed.pr_url).toBe('https://github.com/hollis-labs/torque/pull/42')
    expect(parsed.context.files).toEqual(['apps/gui/src/pages/TaskDetailPage.tsx'])

    fireEvent.click(screen.getByRole('button', { name: 'Request checkpoint' }))

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled()
    })

    const call = vi.mocked(globalThis.fetch).mock.calls[0]
    expect(String(call[0])).toBe('/api/v1/checkpoints')
    expect(JSON.parse(String((call[1] as RequestInit).body))).toEqual({
      task_id: 'CW-20260510-0120',
      type: 'pr_review',
      payload_json: payload.value,
      emitter_source_type: 'user',
      emitter_source_ref: 'gui',
    })
    expect(onRequested).toHaveBeenCalledWith(expect.objectContaining({ correlation_id: 'corr-1' }))
  })

  it('requires payload JSON to be an object', async () => {
    render(
      <ApiProvider>
        <TaskHITLRequestDialog
          task={makeTask()}
          artifacts={[makeArtifact()]}
          open
          onOpenChange={() => {}}
        />
      </ApiProvider>,
    )

    const payload = screen.getByLabelText('Payload JSON') as HTMLTextAreaElement
    fireEvent.change(payload, { target: { value: '[]' } })

    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Request checkpoint' }).disabled).toBe(true)
  })
})
