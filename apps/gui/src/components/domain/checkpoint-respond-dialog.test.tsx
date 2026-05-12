// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ApiProvider } from '@/hooks/use-api'
import type { Checkpoint } from '@/lib/types'
import { CheckpointRespondDialog } from './checkpoint-respond-dialog'

vi.mock('@/lib/toast', () => ({
  notifyError: vi.fn(),
  notifySuccess: vi.fn(),
}))

const baseCheckpoint: Checkpoint = {
  id: 1,
  task_id: 'TASK-1',
  run_id: null,
  correlation_id: 'corr-1',
  type: 'approval',
  payload_json: '{"title":"Deploy","prompt":"Deploy now?"}',
  response_json: null,
  emitter_source_type: 'agent',
  emitter_source_ref: null,
  responder_source_type: null,
  responder_source_ref: null,
  emitted_at: '2026-05-11T00:00:00Z',
  responded_at: null,
  timeout_at: null,
  status: 'pending',
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderDialog(checkpoint: Checkpoint) {
  return render(
    <ApiProvider baseUrl="/api/v1">
      <CheckpointRespondDialog
        checkpoint={checkpoint}
        open
        onOpenChange={() => {}}
      />
    </ApiProvider>,
  )
}

function findRespondBody(): {
  response_json: string
  responder_source_type: string
  responder_source_ref?: string
} {
  const fetchMock = vi.mocked(globalThis.fetch)
  const call = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/respond'))
  expect(call).toBeTruthy()
  return JSON.parse(String((call?.[1] as RequestInit).body))
}

describe('CheckpointRespondDialog', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    globalThis.fetch = vi.fn(() => Promise.resolve(jsonResponse(baseCheckpoint))) as unknown as typeof fetch
  })

  afterEach(() => {
    cleanup()
  })

  it('renders a preset form for approval and serializes it to response_json', async () => {
    renderDialog(baseCheckpoint)

    expect(screen.getByText(/Task TASK-1/i).textContent).toContain('Approval')
    expect(screen.queryByLabelText(/Response \(JSON\)/i)).toBeNull()

    fireEvent.change(screen.getByLabelText('Comment'), {
      target: { value: 'Approved for this sprint.' },
    })
    fireEvent.change(screen.getByLabelText('Responder ref'), {
      target: { value: 'worker-2' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Respond' }))

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled()
    })

    const body = findRespondBody()
    expect(JSON.parse(body.response_json)).toEqual({
      decision: 'approved',
      comment: 'Approved for this sprint.',
    })
    expect(body.responder_source_type).toBe('user')
    expect(body.responder_source_ref).toBe('worker-2')
  })

  it('serializes pull request review line fields as arrays', async () => {
    renderDialog({
      ...baseCheckpoint,
      type: 'pr_review',
      payload_json: '{"pr_url":"https://example.test/pull/1"}',
    })

    expect(screen.queryByLabelText(/Response \(JSON\)/i)).toBeNull()

    fireEvent.change(screen.getByLabelText('Summary'), {
      target: { value: 'Looks ready.' },
    })
    fireEvent.change(screen.getByLabelText('Comments'), {
      target: { value: 'Good tests\nClear diff\n' },
    })
    fireEvent.change(screen.getByLabelText('Required changes'), {
      target: { value: 'Update changelog\n\nConfirm rollout' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Respond' }))

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled()
    })

    expect(JSON.parse(findRespondBody().response_json)).toEqual({
      decision: 'approve',
      summary: 'Looks ready.',
      comments: ['Good tests', 'Clear diff'],
      required_changes: ['Update changelog', 'Confirm rollout'],
    })
  })

  it('renders message acknowledgment controls and serializes the response', async () => {
    renderDialog({
      ...baseCheckpoint,
      type: 'message',
      payload_json: '{"message":"Heads up"}',
    })

    expect(screen.queryByLabelText(/Response \(JSON\)/i)).toBeNull()
    expect(screen.getByRole('switch', { name: 'Acknowledged' })).toBeTruthy()

    fireEvent.change(screen.getByLabelText('Reply'), {
      target: { value: 'Received.' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Respond' }))

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled()
    })

    expect(JSON.parse(findRespondBody().response_json)).toEqual({
      acknowledged: true,
      reply: 'Received.',
    })
  })

  it('renders a generic form for unknown workflow types without exposing JSON', async () => {
    renderDialog({
      ...baseCheckpoint,
      type: 'vendor.custom',
      payload_json: '{"opaque":true}',
    })

    expect(screen.queryByLabelText(/JSON/i)).toBeNull()
    fireEvent.change(screen.getByLabelText('Response *'), { target: { value: 'ok' } })
    fireEvent.click(screen.getByRole('button', { name: 'Respond' }))

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled()
    })

    expect(JSON.parse(findRespondBody().response_json)).toEqual({ response: 'ok' })
  })
})
