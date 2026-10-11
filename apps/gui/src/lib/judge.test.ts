import { expect, it } from 'vitest'
import { acceptanceRate, bandOf, groupByBand, pct, questionLabel } from '@/lib/judge'
import type { Suggestion } from '@/lib/types'

const sg = (id: number, confidence: number): Suggestion => ({
  id, subject_type: 'task', subject_id: `T-${id}`, set_id: 'task-kind', question_key: 'kind', answer_type: 'choice',
  confidence, suggested: ['bug'], provider: 'p', model: 'm', disposition: 'suggested', created_at: '',
})

it('puts a confidence in the band whose lower bound it meets, upper bound exclusive', () => {
  expect(bandOf(0.9).key).toBe('very-high')
  expect(bandOf(0.8999).key).toBe('high')
  expect(bandOf(0.8).key).toBe('high')
  expect(bandOf(0.7).key).toBe('medium')
  expect(bandOf(0.12).key).toBe('low')
  expect(bandOf(1).key).toBe('very-high')
})

it('groups highest band first and leaves out empty bands', () => {
  const groups = groupByBand([sg(1, 0.72), sg(2, 0.95), sg(3, 0.93)])
  expect(groups.map((g) => g.band.key)).toEqual(['very-high', 'medium'])
  expect(groups[0].items.map((s) => s.id)).toEqual([2, 3])
})

it('computes the acceptance rate from decided rows only', () => {
  expect(acceptanceRate(undefined)).toBeNull()
  expect(acceptanceRate({ suggested: 9 })).toBeNull()
  expect(acceptanceRate({ accepted: 3, rejected: 1, suggested: 50 })).toBe(0.75)
  expect(acceptanceRate({ auto_applied: 1, rejected: 1 })).toBe(0.5)
})

it('formats percentages and question labels', () => {
  expect(pct(0.846)).toBe('85%')
  expect(questionLabel('task-decision-state', 'discuss_first')).toBe('decision-state · discuss first')
})
