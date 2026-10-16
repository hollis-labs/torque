// Wiring hooks: the WorkflowClient context and the live run list.
import { createContext, useContext, useEffect, useMemo, useState } from 'react'
import type { WorkflowClient } from './client'
import type { WorkflowRun } from './types'

export const WorkflowClientContext = createContext<WorkflowClient | null>(null)

export function useWorkflowClient(): WorkflowClient {
  const c = useContext(WorkflowClientContext)
  if (!c) throw new Error('useWorkflowClient must be used inside <WorkflowProvider>')
  return c
}

export function useWorkflowRuns(client: WorkflowClient): WorkflowRun[] {
  const [items, setItems] = useState<Record<string, WorkflowRun>>({})
  useEffect(() => {
    let live = true
    void client.list().then((all) => live && setItems((cur) => ({ ...Object.fromEntries(all.map((r) => [r.id, r])), ...cur })))
    const off = client.subscribe((changed) => setItems((cur) => ({ ...cur, ...Object.fromEntries(changed.map((r) => [r.id, r])) })))
    return () => {
      live = false
      off()
    }
  }, [client])
  return useMemo(() => Object.values(items).sort((a, b) => b.updatedAt - a.updatedAt), [items])
}
