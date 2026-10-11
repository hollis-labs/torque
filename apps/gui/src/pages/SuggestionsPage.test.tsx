/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Suggestion } from '@/lib/types'

const { api } = vi.hoisted(() => ({
  api: {
    judgeSuggestions: vi.fn(),
    judgeStatus: vi.fn(),
    judgeSummary: vi.fn(),
    judgeAccept: vi.fn(),
    judgeReject: vi.fn(),
    judgeBulk: vi.fn(),
  },
}))
vi.mock('@/hooks/use-api', () => ({ useApi: () => api }))
vi.mock('@/hooks/use-sse', () => ({ useSSE: () => ({ lastEvent: null }) }))
import SuggestionsPage from './SuggestionsPage'

const sg = (id: number, confidence: number, over: Partial<Suggestion> = {}): Suggestion => ({
  id, subject_type: 'task', subject_id: `CW-${id}`, set_id: 'task-kind', question_key: 'kind', answer_type: 'choice',
  confidence, suggested: ['bug'], provider: 'jev', model: 'jev-1.13.0', disposition: 'suggested', created_at: '',
  task_title: `Task ${id}`, task_tags: ['backend'], ...over,
})

const renderPage = () => render(<MemoryRouter><SuggestionsPage /></MemoryRouter>)

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset()
  api.judgeSuggestions.mockResolvedValue([sg(1, 0.95), sg(2, 0.93), sg(3, 0.72)])
  api.judgeStatus.mockResolvedValue({ feature_enabled: false, decider_configured: true, decider: 'tether', active: false })
  api.judgeSummary.mockResolvedValue({ 'task-kind': { kind: { accepted: 3, rejected: 1, suggested: 3 } } })
  api.judgeBulk.mockResolvedValue({ decided: 2 })
  api.judgeAccept.mockResolvedValue({ accepted: 3, added_tags: ['bug'] })
})
afterEach(cleanup)

it('groups suggestions by confidence band and shows how often a question is accepted', async () => {
  renderPage()
  const high = await screen.findByRole('region', { name: 'Confidence 90% and up' })
  expect(within(high).getAllByTestId('suggestion-row')).toHaveLength(2)
  expect(screen.getByRole('region', { name: 'Confidence 70–80%' })).toBeTruthy()
  expect(screen.getByText(/kind: 75%/)).toBeTruthy()
  expect(screen.getByText(/turn on the “judge” feature/)).toBeTruthy()
})

it('asks twice before tagging a whole band, then decides exactly that band', async () => {
  renderPage()
  const high = await screen.findByRole('region', { name: 'Confidence 90% and up' })
  fireEvent.click(within(high).getByRole('button', { name: 'Accept all 2' }))
  expect(api.judgeBulk).not.toHaveBeenCalled()
  fireEvent.click(within(high).getByRole('button', { name: 'Confirm: tag 2 tasks' }))
  await waitFor(() => expect(api.judgeBulk).toHaveBeenCalledTimes(1))
  expect(api.judgeBulk).toHaveBeenCalledWith({ action: 'accept', set: undefined, question: undefined, min: 0.9, max: undefined, limit: 2 })
})

it('accepts one suggestion without touching the others', async () => {
  renderPage()
  const btn = await screen.findByRole('button', { name: 'Accept bug for CW-3' })
  fireEvent.click(btn)
  await waitFor(() => expect(api.judgeAccept).toHaveBeenCalledWith(3))
  expect(api.judgeBulk).not.toHaveBeenCalled()
})

it('explains how to get suggestions when there are none', async () => {
  api.judgeSuggestions.mockResolvedValue([])
  renderPage()
  expect(await screen.findByText('No open suggestions')).toBeTruthy()
})
