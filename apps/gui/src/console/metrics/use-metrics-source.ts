import { createContext, useContext, useMemo } from 'react'
import { useApi } from '@/hooks/use-api'
import { TorqueMetricsSource, type TorqueMetricsClient } from './torque-source'
import type { MetricsSource } from './types'

export const SourceContext = createContext<MetricsSource | null>(null)

/** Context override, else a Torque-backed source on the app's API client. No fixture fallback: failures show honestly. */
export function useMetricsSource(): MetricsSource {
  const ctx = useContext(SourceContext)
  const api = useApi() as unknown as TorqueMetricsClient
  return useMemo(() => ctx ?? new TorqueMetricsSource(api), [ctx, api])
}

