import type { ReactNode } from 'react'
import { SourceContext } from './use-metrics-source'
import type { MetricsSource } from './types'

/** Override the metrics source for a subtree (tests, previews, a future bus-backed source). */
export function MetricsSourceProvider({ source, children }: { source: MetricsSource; children: ReactNode }) {
  return <SourceContext.Provider value={source}>{children}</SourceContext.Provider>
}
