import { useEffect, useState } from 'react'
import type { MetricsLoad, MetricsSource, ProjectMetrics } from './types'

/** Loads from a MetricsSource and exposes loading/error/ready. Aborts on unmount or scope change. */
export function useMetrics(source: MetricsSource, scope?: string): MetricsLoad<ProjectMetrics> {
  const [state, setState] = useState<{ key: string; value: MetricsLoad<ProjectMetrics> } | null>(null)
  const key = `${scope ?? ''}`
  useEffect(() => {
    const ctl = new AbortController()
    source.load(scope, ctl.signal).then(
      (data) => !ctl.signal.aborted && setState({ key, value: { status: 'ready', data } }),
      (e: unknown) => !ctl.signal.aborted && setState({ key, value: { status: 'error', message: e instanceof Error ? e.message : String(e) } }),
    )
    return () => ctl.abort()
  }, [source, scope, key])
  return state && state.key === key ? state.value : { status: 'loading' }
}
