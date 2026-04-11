import { describe, it, expect } from 'vitest'
import {
  formatCostBudget,
  formatDurationMs,
  formatTokenBudget,
} from './sentinel-display'

describe('formatCostBudget', () => {
  it('null → default', () => {
    expect(formatCostBudget(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatCostBudget(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('0 → $0.00', () => {
    expect(formatCostBudget(0)).toEqual({ label: '$0.00', mode: 'value' })
  })
  it('5 → $5.00', () => {
    expect(formatCostBudget(5)).toEqual({ label: '$5.00', mode: 'value' })
  })
  it('12.5 → $12.50', () => {
    expect(formatCostBudget(12.5)).toEqual({ label: '$12.50', mode: 'value' })
  })
})

describe('formatDurationMs', () => {
  it('null → default', () => {
    expect(formatDurationMs(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatDurationMs(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('1000 → 1s', () => {
    expect(formatDurationMs(1000)).toEqual({ label: '1s', mode: 'value' })
  })
  it('60000 → 1m', () => {
    expect(formatDurationMs(60000)).toEqual({ label: '1m', mode: 'value' })
  })
  it('65000 → 1m 5s', () => {
    expect(formatDurationMs(65000)).toEqual({ label: '1m 5s', mode: 'value' })
  })
  it('3600000 → 1h', () => {
    expect(formatDurationMs(3600000)).toEqual({ label: '1h', mode: 'value' })
  })
  it('3665000 → 1h 1m 5s', () => {
    expect(formatDurationMs(3665000)).toEqual({ label: '1h 1m 5s', mode: 'value' })
  })
  // cost_budget allows explicit 0, duration does not — but the function
  // treats it as a valid positive-or-zero value for display purposes
  it('0 → 0s', () => {
    expect(formatDurationMs(0)).toEqual({ label: '0s', mode: 'value' })
  })
})

describe('formatTokenBudget', () => {
  it('null → default', () => {
    expect(formatTokenBudget(null)).toEqual({ label: 'default', mode: 'default' })
  })
  it('-1 → unlimited', () => {
    expect(formatTokenBudget(-1)).toEqual({ label: 'unlimited', mode: 'unlimited' })
  })
  it('1000 → 1,000', () => {
    expect(formatTokenBudget(1000)).toEqual({ label: '1,000', mode: 'value' })
  })
  it('1000000 → 1,000,000', () => {
    expect(formatTokenBudget(1000000)).toEqual({ label: '1,000,000', mode: 'value' })
  })
})
