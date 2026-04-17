import { describe, it, expect } from 'vitest'
import {
  hasBlockedReason,
  truncateBlockedReason,
  BLOCKED_REASON_TOOLTIP_LIMIT,
} from './blocked-reason'
import type { Task } from './types'

function task(status: Task['status'], reason: string): Pick<Task, 'status' | 'blocked_reason'> {
  return { status, blocked_reason: reason }
}

describe('hasBlockedReason', () => {
  it('true when status=blocked with non-empty reason', () => {
    expect(hasBlockedReason(task('blocked', 'permission denied'))).toBe(true)
  })

  it('true when status=paused with non-empty reason', () => {
    expect(hasBlockedReason(task('paused', 'awaiting human input'))).toBe(true)
  })

  it('false when reason is whitespace only', () => {
    expect(hasBlockedReason(task('blocked', '   \n\t '))).toBe(false)
  })

  it('false when status is not blocked or paused', () => {
    expect(hasBlockedReason(task('done', 'old reason'))).toBe(false)
    expect(hasBlockedReason(task('doing', 'in progress'))).toBe(false)
  })

  it('false when reason is empty string', () => {
    expect(hasBlockedReason(task('blocked', ''))).toBe(false)
  })
})

describe('truncateBlockedReason', () => {
  it('returns short reasons unchanged', () => {
    expect(truncateBlockedReason('short reason')).toBe('short reason')
  })

  it('trims surrounding whitespace before counting', () => {
    expect(truncateBlockedReason('  hello  ')).toBe('hello')
  })

  it('appends ellipsis when over the default limit', () => {
    const reason = 'a'.repeat(BLOCKED_REASON_TOOLTIP_LIMIT + 50)
    const out = truncateBlockedReason(reason)
    expect(out.endsWith('…')).toBe(true)
    expect(out.length).toBe(BLOCKED_REASON_TOOLTIP_LIMIT + 1)
  })

  it('respects an explicit limit', () => {
    expect(truncateBlockedReason('abcdefghij', 5)).toBe('abcde…')
  })

  it('does not truncate exactly at the limit', () => {
    const reason = 'x'.repeat(BLOCKED_REASON_TOOLTIP_LIMIT)
    expect(truncateBlockedReason(reason)).toBe(reason)
  })
})
