import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { TorqueApiClient } from '@/lib/api'

const ApiContext = createContext<TorqueApiClient | null>(null)

interface ApiProviderProps {
  baseUrl?: string
  children: ReactNode
}

export function ApiProvider({ baseUrl, children }: ApiProviderProps) {
  const client = useMemo(
    () => new TorqueApiClient(baseUrl),
    [baseUrl]
  )

  return <ApiContext.Provider value={client}>{children}</ApiContext.Provider>
}

export function useApi(): TorqueApiClient {
  const client = useContext(ApiContext)
  if (!client) {
    throw new Error('useApi must be used within an ApiProvider')
  }
  return client
}
