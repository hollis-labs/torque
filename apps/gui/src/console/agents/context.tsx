import { createContext, useContext, type ReactNode } from 'react'
import type { AgentsClient } from './client'
import { createMockAgentsClient } from './mock'

const defaultClient = createMockAgentsClient()
const AgentsClientContext = createContext<AgentsClient>(defaultClient)

/** Swap the client (tests, the live adapter) without touching the views. */
export function AgentsClientProvider({ client, children }: { client: AgentsClient; children: ReactNode }) {
  return <AgentsClientContext.Provider value={client}>{children}</AgentsClientContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useAgentsClient = () => useContext(AgentsClientContext)
