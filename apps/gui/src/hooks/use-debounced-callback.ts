import { useEffect, useRef, useState } from 'react'
import { createTrailingDebounce } from '@/lib/debounce'

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
  const [debounce] = useState(() => createTrailingDebounce(() => fnRef.current(), delayMs, maxWaitMs))
  useEffect(() => debounce.cancel, [debounce])
  return debounce.schedule
}
