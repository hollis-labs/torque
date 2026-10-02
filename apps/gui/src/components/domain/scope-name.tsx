import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useApi } from '@/hooks/use-api'
import type { ScopeKind } from './scope-picker'

const NameContext = createContext<{ kind: ScopeKind; names: Map<string, string> } | null>(null)

/** Resolve only distinct IDs on the rendered page; never load an option list. */
export function ScopeNames({ kind, ids, children }: { kind: ScopeKind; ids: string[]; children: ReactNode }) {
  const api = useApi()
  const key = JSON.stringify([...new Set(ids)].sort())
  const [state, setState] = useState<{ key: string; names: Map<string, string> } | null>(null)
  useEffect(() => {
    let active = true
    const requested: string[] = JSON.parse(key)
    void Promise.all(requested.map(async id => {
      try {
        const record = kind === 'project' ? await api.getProject(id) : kind === 'epic' ? await api.getEpic(id) : await api.getSprint(id)
        return [id, record.name] as const
      } catch { return [id, id] as const }
    })).then(names => { if (active) setState({ key, names: new Map(names) }) })
    return () => { active = false }
  }, [api, kind, key])
  return <NameContext.Provider value={{ kind, names: state?.key === key ? state.names : new Map() }}>{children}</NameContext.Provider>
}

export function ScopeName({ kind, id }: { kind: ScopeKind; id: string }) {
  const context = useContext(NameContext)
  return <>{context?.kind === kind ? context.names.get(id) ?? id : id}</>
}
