import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { ClockworkApiClient } from '@/lib/api'

const ApiContext = createContext<ClockworkApiClient | null>(null)

interface ApiProviderProps {
  baseUrl?: string
  children: ReactNode
}

export function ApiProvider({ baseUrl, children }: ApiProviderProps) {
  const client = useMemo(
    () => new ClockworkApiClient(baseUrl),
    [baseUrl]
  )

  return <ApiContext.Provider value={client}>{children}</ApiContext.Provider>
}

export function useApi(): ClockworkApiClient {
  const client = useContext(ApiContext)
  if (!client) {
    throw new Error('useApi must be used within an ApiProvider')
  }
  return client
}
