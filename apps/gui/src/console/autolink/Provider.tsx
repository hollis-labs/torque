import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  buildPreviewCard, createRefResolverClient, getRefRegistry,
  type KindResolver, type RefIdentity, type RefRegistry, type RefResolverClient,
} from '../refs'
import { AutolinkContext, type ActivateEvent, type AutolinkContextValue } from './context'
import { RefDrawer } from './RefDrawer'

export interface AutolinkProviderProps {
  /** Defaults to the app-wide registry (`getRefRegistry()`). */
  registry?: RefRegistry
  /** Resolvers by kind id. Memoise: a new object builds a new client and drops the cache. */
  resolvers?: Record<string, KindResolver | undefined>
  /** Bring your own client (tests, a shared page-level client). Wins over `resolvers`. */
  client?: RefResolverClient
  /** Override client-side navigation. Defaults to react-router's navigate. */
  navigate?: (to: string) => void
  children: ReactNode
}

/**
 * Hosts resolution, the hovercard defaults and the detail drawer for every
 * Linkified / RefChip below it. Mount once per page or (R2) once at the shell;
 * it opens no streams and does no work until a chip renders.
 */
export function AutolinkProvider({ registry, resolvers, client, navigate, children }: AutolinkProviderProps) {
  const routerNavigate = useNavigate()
  const go = navigate ?? routerNavigate
  const reg = useMemo(() => registry ?? getRefRegistry(), [registry])
  const owned = useMemo(() => client ?? createRefResolverClient({ resolvers: resolvers ?? {} }), [client, resolvers])
  const [drawerRef, setDrawerRef] = useState<RefIdentity | null>(null)

  const activate = useCallback(
    (ref: RefIdentity, ev: ActivateEvent) => {
      if (ev.modified) return false
      const state = owned.get(ref.kind, ref.canonical)
      const open = buildPreviewCard(reg, ref, state).open
      if (open.type === 'external') return false
      // No resolver means the drawer would only repeat the chip: go to the page.
      if (open.type === 'route' && state.status === 'unsupported') {
        go(open.to)
        return true
      }
      setDrawerRef(ref)
      return true
    },
    [owned, reg, go],
  )

  const value = useMemo<AutolinkContextValue>(
    () => ({
      registry: reg, client: owned, activate, navigate: go, hosted: true,
      openDrawer: setDrawerRef,
      closeDrawer: () => setDrawerRef(null),
    }),
    [reg, owned, activate, go],
  )

  return (
    <AutolinkContext.Provider value={value}>
      {children}
      <RefDrawer target={drawerRef} onClose={() => setDrawerRef(null)} />
    </AutolinkContext.Provider>
  )
}
