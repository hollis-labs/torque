import { describe, expect, it } from 'vitest'
import { accumulateTokenTotals, type TokenTotals } from './active-runs-context'

// Deltas modeled on a real OpenCode two-tool turn: three step_finish usage
// events (tether-3a's opencode 1.18.30 sample). tokens.total is each step's
// context size and must never be summed.
const steps = [
  { prompt: 11200, completion: 61, cache_read: 0, cache_write: 11000, cost: 0.19, total: 22261 },
  { prompt: 180, completion: 48, cache_read: 11000, cache_write: 200, cost: 0.02, total: 22428 },
  { prompt: 90, completion: 4, cache_read: 11200, cache_write: 0, cost: 0.016, total: 22494 },
]

function fold(payloads: Record<string, unknown>[]): TokenTotals | undefined {
  return payloads.reduce<TokenTotals | undefined>(
    (acc, p) => accumulateTokenTotals(acc, p),
    undefined,
  )
}

describe('accumulateTokenTotals', () => {
  it('sums a multi-step turn instead of keeping the last step', () => {
    const got = fold(steps)!
    expect(got.prompt).toBe(11470)
    expect(got.completion).toBe(113)
    expect(got.cache_read).toBe(22200)
    expect(got.cache_write).toBe(11200)
    expect(got.cost).toBeCloseTo(0.226, 6)
    expect(got).not.toHaveProperty('total')
  })

  it('leaves a single-event turn unchanged', () => {
    expect(fold([{ prompt: 1200, completion: 340, cost: 0 }])).toEqual({
      prompt: 1200,
      completion: 340,
      cache_read: 0,
      cache_write: 0,
      cost: 0,
    })
  })

  it('prefers the server running total over summing received deltas', () => {
    // Steps 2 and 3 were throttled away; the next emission carries totals
    // that include them. Summing what arrived would under-count.
    const afterFirst = accumulateTokenTotals(undefined, {
      ...steps[0],
      totals: { prompt: 11200, completion: 61, cache_read: 0, cache_write: 11000, cost: 0.19 },
    })
    const got = accumulateTokenTotals(afterFirst, {
      prompt: 0,
      completion: 1,
      cost: 0,
      totals: { prompt: 11470, completion: 114, cache_read: 22200, cache_write: 11200, cost: 0.226 },
    })
    expect(got).toEqual({ prompt: 11470, completion: 114, cache_read: 22200, cache_write: 11200, cost: 0.226 })
  })

  it('never reads a context-size total field', () => {
    const got = fold([{ total: 50000 }, { total: 60000 }])!
    expect(Object.values(got).every((v) => v === 0)).toBe(true)
  })

  it('treats junk values as zero', () => {
    expect(fold([{ prompt: 'abc', completion: null }])).toEqual({
      prompt: 0,
      completion: 0,
      cache_read: 0,
      cache_write: 0,
      cost: 0,
    })
  })
})
