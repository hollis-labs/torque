import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTrailingDebounce } from './debounce'

describe('createTrailingDebounce', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('collapses a burst into one trailing call', () => {
    const fn = vi.fn()
    const d = createTrailingDebounce(fn, 1000)
    for (let i = 0; i < 20; i++) {
      d.schedule()
      vi.advanceTimersByTime(50)
    }
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('fires by maxWait under a steady stream, then rearms', () => {
    const fn = vi.fn()
    const d = createTrailingDebounce(fn, 1000, 3000)
    for (let i = 0; i < 10; i++) {
      d.schedule()
      vi.advanceTimersByTime(500)
    }
    // 5 s of calls every 500 ms: the quiet window never elapses, maxWait
    // fires once at 3 s and the next burst rearms it.
    expect(fn).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('cancel drops the pending call', () => {
    const fn = vi.fn()
    const d = createTrailingDebounce(fn, 1000)
    d.schedule()
    d.cancel()
    vi.advanceTimersByTime(5000)
    expect(fn).not.toHaveBeenCalled()
  })
})
