import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

import { toast } from 'sonner'
import { notifyError, notifySuccess } from './toast'

describe('notifyError', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uses Error.message when err is an Error with a non-empty message', () => {
    notifyError(new Error('boom'), 'fallback')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('boom')
  })

  it('uses the string itself when err is a non-empty string', () => {
    notifyError('explicit string', 'fallback')
    expect(toast.error).toHaveBeenCalledWith('explicit string')
  })

  it('uses the fallback when err is null/undefined/object/number', () => {
    notifyError(null, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError(undefined, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError({}, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')

    notifyError(42, 'fallback')
    expect(toast.error).toHaveBeenLastCalledWith('fallback')
  })

  it('uses the fallback when err is an Error with an empty message', () => {
    notifyError(new Error(''), 'fallback')
    expect(toast.error).toHaveBeenCalledWith('fallback')
  })
})

describe('notifySuccess', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('forwards the message to toast.success', () => {
    notifySuccess('all good')
    expect(toast.success).toHaveBeenCalledTimes(1)
    expect(toast.success).toHaveBeenCalledWith('all good')
  })
})
