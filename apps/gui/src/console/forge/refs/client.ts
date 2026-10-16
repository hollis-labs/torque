import { getRefRegistry, type RefRegistry } from '../../refs'
import type { ForgeClient } from '../client'
import { createDefaultForgeClient } from '../factory'

// The forge refs read through whichever ForgeClient is installed here. The
// default is the factory's choice (mock unless `?forge=http` / VITE_FORGE_SOURCE),
// so switching to G1's HttpForgeClient needs no change in this module.
let current: ForgeClient | null = null

export function getForgeClient(): ForgeClient {
  return (current ??= createDefaultForgeClient())
}

/** Install a client (tests, Z1 live swap, a shared app-level instance). Returns a restore function. */
export function setForgeClient(client: ForgeClient | null): () => void {
  const prev = current
  current = client
  return () => {
    if (current === client) current = prev
  }
}

export type { RefRegistry }
export { getRefRegistry }
