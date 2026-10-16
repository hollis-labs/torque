import { createTorqueTaskResolver, type KindResolver } from '../refs'
import type { TorqueApiClient } from '@/lib/api'
import { registerForgeRefs } from '../forge/refs'

/**
 * Resolvers that talk to the real backend today: only `torque.task`, through
 * the existing `/planner/torque-titles` batch endpoint. Forge kinds (G2) resolve through the ForgeClient. Every other kind stays
 * unresolved (chip + link, no preview) until its endpoint exists; see BACKEND_NEEDS.md.
 * Memoise the result before handing it to AutolinkProvider.
 */
export function createLiveResolvers(api: Pick<TorqueApiClient, 'plannerTaskTitles'>): Record<string, KindResolver> {
  // Forge kinds (G2): registers pr/commit/repo (idempotent) and resolves through the ForgeClient (mock by default).
  const forge = registerForgeRefs().resolvers
  return { ...forge, 'torque.task': createTorqueTaskResolver((ids, signal) => api.plannerTaskTitles(ids, signal)) }
}
