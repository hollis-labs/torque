import { useCallback, useEffect, useRef } from 'react'
import { createTrailingDebounce, type TrailingDebounce } from '@/lib/debounce'

/**
 * A stable `schedule()` that runs the latest `fn` once a burst of calls has
 * been quiet for `delayMs` (or `maxWaitMs` has passed since the first call).
 * A pending call is dropped on unmount.
 */
export function useDebouncedCallback(fn: () => void, delayMs: number, maxWaitMs?: number): () => void {
  const fnRef = useRef(fn)
  useEffect(() => {
    fnRef.current = fn
  }, [fn])
  const debounceRef = useRef<TrailingDebounce | null>(null)
  useEffect(() => {
    const debounce = createTrailingDebounce(() => fnRef.current(), delayMs, maxWaitMs)
    debounceRef.current = debounce
    return () => {
      debounce.cancel()
      debounceRef.current = null
    }
  }, [delayMs, maxWaitMs])
  return useCallback(() => debounceRef.current?.schedule(), [])
}
