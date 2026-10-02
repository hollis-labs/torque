import { useEffect, useState } from 'react'
import { useApi } from '@/hooks/use-api'
import type { ScopeKind } from './scope-picker'

/** Resolve a label by ID; a first-page option list is never a name registry. */
export function ScopeName({ kind, id }: { kind: ScopeKind; id: string }) {
  const api = useApi()
  const [record, setRecord] = useState<{ id: string; name: string } | null>(null)
  useEffect(() => {
    let active = true
    const request = kind === 'project' ? api.getProject(id) : kind === 'epic' ? api.getEpic(id) : api.getSprint(id)
    void request.then(item => { if (active) setRecord(item) }).catch(() => {})
    return () => { active = false }
  }, [api, kind, id])
  return <>{record?.id === id ? record.name : id}</>
}
