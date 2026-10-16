import { createContext, useCallback, useContext, useSyncExternalStore } from 'react'
import {
  createRefResolverClient, getRefRegistry,
  type RefIdentity, type RefRegistry, type RefResolverClient, type RefState,
} from '../refs'

export interface ActivateEvent {
  /** Ctrl/Cmd/Shift/Alt or a non-primary button: leave the browser to handle the link. */
  modified: boolean
}

export interface AutolinkContextValue {
  registry: RefRegistry
  client: RefResolverClient
  /** True when the host handled the activation (caller should preventDefault). */
  activate(ref: RefIdentity, ev: ActivateEvent): boolean
  /** Client-side navigation to an app route. */
  navigate(to: string): void
  openDrawer(ref: RefIdentity): void
  closeDrawer(): void
  /** False outside an AutolinkProvider: chips degrade to plain links. */
  hosted: boolean
}

// Outside a provider: shared registry, a client with no resolvers (every ref
// is "unresolved"), no drawer. Linkified still detects and links.
let fallback: AutolinkContextValue | null = null
export function defaultAutolinkContext(): AutolinkContextValue {
  if (!fallback) {
    fallback = {
      registry: getRefRegistry(),
      client: createRefResolverClient({ resolvers: {} }),
      activate: () => false,
      navigate: (to) => window.location.assign(to),
      openDrawer: () => undefined,
      closeDrawer: () => undefined,
      hosted: false,
    }
  }
  return fallback
}

export const AutolinkContext = createContext<AutolinkContextValue | null>(null)

export function useAutolink(): AutolinkContextValue {
  return useContext(AutolinkContext) ?? defaultAutolinkContext()
}

/** Subscribe to one reference's resolver state. */
export function useRefState(kind: string, canonical: string): RefState {
  const { client } = useAutolink()
  const get = useCallback(() => client.get(kind, canonical), [client, kind, canonical])
  return useSyncExternalStore(client.subscribe, get, get)
}
